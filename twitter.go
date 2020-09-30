// Copyright (C) 2020 iDigitalFlame
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.
//

package watcher

import (
	"context"
	"strconv"

	"github.com/dghubble/go-twitter/twitter"
)

func (w *Watcher) threadSubscribe(x context.Context) {
	w.wg.Add(1)
	go w.handleResolve(x, false)
	for s := w.watcher(x); ; s = w.watcher(x) {
		select {
		case <-w.reload:
			if s != nil {
				s.Stop()
			}
		case <-x.Done():
			w.log.Debug("Stopping Twitter subscriber thread...")
			if s != nil {
				s.Stop()
			}
			w.wg.Done()
			return
		case <-w.resolve:
			go w.handleResolve(x, false)
		case <-w.update.C:
			go w.handleResolve(x, true)
		}
	}
}
func watch(c chan<- *twitter.Tweet, s *twitter.Stream) {
	for m := range s.Messages {
		if t, ok := m.(*twitter.Tweet); ok {
			if !t.Retweeted && t.RetweetedStatus == nil && len(t.QuotedStatusIDStr) == 0 && len(t.InReplyToStatusIDStr) == 0 {
				c <- t
			}
		}
	}
}
func (w *Watcher) handleResolve(x context.Context, a bool) {
	w.wg.Add(1)
	w.log.Debug("Starting Twitter mapping resolve task...")
	r, err := w.sql.QueryContext(x, "get_all")
	if err != nil {
		w.log.Error("Error during resolving Twitter names (DB): %s!", err.Error())
		return
	}
	var (
		o    = make([]resolve, 0, 64)
		s    string
		i, m int64
	)
	for r.Next() {
		if err := r.Scan(&i, &s, &m); err != nil {
			w.log.Error("Error during resolving Twitter names (DB Scan Entry): %s!", err.Error())
			continue
		}
		if !a && m != 0 || len(s) == 0 {
			continue
		}
		o = append(o, resolve{id: i, tid: m, name: s})
	}
	if r.Close(); len(o) == 0 {
		w.log.Debug("List of names to resolve is empty, skipping non-scheduled resolve.")
		w.wg.Done()
		return
	}
	w.log.Trace("Resolve list gathered, attempting to resolve %d usernames...", len(o))
	for l, i, z := make([]string, 0, 100), 0, 0; i < len(o); i += 100 {
		if z = i + 100; z > len(o) {
			z = len(o)
		}
		for _, v := range o[i:z] {
			l = append(l, v.name)
		}
		u, _, err := w.twitter.Users.Lookup(
			&twitter.UserLookupParams{ScreenName: l, IncludeEntities: twitter.Bool(false)},
		)
		if err != nil {
			w.log.Error("Error during resolving Twitter names (Twitter Lookup): %s!", err.Error())
			continue
		}
		for x := range u {
			for v := range o[i:z] {
				if matchLower(u[x].ScreenName, o[v].name) {
					w.log.Trace("Twitter username %q was resolved to ID %q...", u[x].ScreenName, u[x].IDStr)
					o[v].tid = u[x].ID
					break
				}
			}
		}
	}
	for i := range o {
		if _, err := w.sql.Exec("set", o[i].tid, o[i].id); err != nil {
			w.log.Error("Error during resolving Twitter names (DB Update Entry): %s!", err.Error())
			continue
		}
		w.log.Trace("Updated Twitter mapping %s: %d => %d.", o[i].name, o[i].id, o[i].tid)
	}
	w.log.Debug("Completed Twitter Mapping resolve task!")
	w.reload <- wake
	w.wg.Done()
}
func (w *Watcher) watcher(x context.Context) *twitter.Stream {
	r, err := w.sql.QueryContext(x, "get_list")
	if err != nil {
		w.log.Error("Error Twitter subscriber generation (DB): %s!", err.Error())
		return nil
	}
	var (
		s int64
		o = make([]string, 0, 64)
	)
	for r.Next() {
		if err := r.Scan(&s); err != nil {
			w.log.Error("Error Twitter subscriber generation (DB Scan ENtry): %s!", err.Error())
			continue
		}
		if s == 0 {
			continue
		}
		o = append(o, strconv.Itoa(int(s)))
	}
	if r.Close(); len(o) == 0 {
		w.log.Debug("List of subscribed accounts is empty, not watching Twitter.")
		return nil
	}
	w.log.Trace("Subscribed list generated, watching %d users...", len(o))
	f, err := w.twitter.Streams.Filter(
		&twitter.StreamFilterParams{Follow: o, Language: []string{"en"}, StallWarnings: twitter.Bool(true)},
	)
	if err != nil {
		w.log.Error("Error Twitter subscriber generation (Stream Filter): %s!", err.Error())
		return nil
	}
	w.log.Debug("Twitter stream subscriber thread started!")
	go watch(w.tweets, f)
	return f
}
