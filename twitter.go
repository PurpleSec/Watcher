// Copyright 2021 - 2022 PurpleSec Team
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
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/dghubble/go-twitter/twitter"
)

const (
	drop  = time.Minute
	pause = time.Second * 5
)

type mapping struct {
	New, Name   string
	ID, Twitter int64
}

func (w *Watcher) resolve(x context.Context, a bool) {
	w.log.Info("Starting Twitter ID mapping resolve task..")
	r, err := w.sql.QueryContext(x, "get_all")
	if err != nil {
		w.log.Error("Error getting Twitter mappings from database: %s!", err.Error())
		return
	}
	var (
		l       = make([]*mapping, 0, 64)
		s       string
		t, m, u int64
	)
	for r.Next() {
		if err = r.Scan(&t, &m, &s, &u); err != nil {
			w.log.Error("Error scanning data into Twitter mappings from database: %s!", err.Error())
			continue
		}
		if !a && u != 0 || len(s) == 0 {
			continue
		}
		if cap(l) < int(t) {
			l = append(make([]*mapping, 0, int(t)-cap(l)), l...)
		}
		l = append(l, &mapping{ID: m, Name: s, Twitter: u})
	}
	if r.Close(); len(l) == 0 {
		w.log.Debug("Twitter resolve mapping is empty, not attempting to resolve..")
		return
	}
	var (
		i = make(map[int64]string, len(l))
		p []int64
		n []string
		q []twitter.User
	)
	w.log.Debug("Twitter mapping generated, attempting to resolve to %d user IDs..", len(l))
	for x, z := 0, 0; x < len(l); x += 50 {
		if z = x + 100; z > len(l) {
			z = len(l)
		}
		n = make([]string, 0, z-x)
		for _, v := range l[x:z] {
			n = append(n, v.Name)
		}
		for _, v := range l[x:z] {
			p = append(p, v.Twitter)
		}
		if q, _, err = w.twitter.Users.Lookup(&twitter.UserLookupParams{UserID: p, ScreenName: n}); err != nil {
			w.log.Error("Error retriving data about Twitter mappings from Twitter: %s!", err.Error())
			continue
		}
		for v := range q {
			if _, ok := i[q[v].ID]; ok {
				w.log.Warning("Duplicate ID value %q detected with username %q!", q[v].IDStr, q[v].ScreenName)
			}
			i[q[v].ID] = q[v].ScreenName
		}
	}
	for k, v := range i {
		for x := range l {
			if stringLowMatch(v, l[x].Name) {
				l[x].Twitter = k
				w.log.Trace(`Twitter username %q (db: %s) was resolved to "%d".`, v, l[x].Name, k)
				continue
			}
			if l[x].Twitter == k && !stringLowMatch(v, l[x].Name) {
				l[x].New = v
				w.log.Warning(`Found new name for ID "%d": %s => %s!`, k, l[x].Name, l[x].New)
			}
		}
	}
	for x := range l {
		if len(l[x].New) > 0 {
			_, err = w.sql.Exec("set", l[x].ID, uint64(l[x].Twitter), l[x].New)
		} else {
			_, err = w.sql.Exec("set", l[x].ID, uint64(l[x].Twitter), l[x].Name)
		}
		if err != nil {
			w.log.Error("Error updating Twitter mappings in the database: %s!", err.Error())
			continue
		}
	}
	w.log.Info("Completed Twitter ID mapping resolve task!")
}
func (w *Watcher) stream(x context.Context, f bool, a bool) (*twitter.StreamFilterParams, error) {
	if f {
		w.resolve(x, a)
	}
	r, err := w.sql.QueryContext(x, "get_list")
	if err != nil {
		w.log.Error("Error getting Twitter list from database: %s!", err.Error())
		return nil, err
	}
	var (
		l = make([]string, 0)
		s int64
		c int
	)
	for r.Next() {
		if err = r.Scan(&c, &s); err != nil {
			w.log.Error("Error scanning data into Twitter list from database: %s!", err.Error())
			break
		}
		if s == 0 || c == 0 {
			continue
		}
		if cap(l) < c {
			l = append(make([]string, 0, c-cap(l)), l...)
		}
		l = append(l, strconv.Itoa(int(s)))
	}
	if r.Close(); err != nil {
		return nil, err
	}
	if len(l) == 0 {
		w.log.Info("Twitter watch list is empty, not starting Twitter stream..")
		return nil, nil
	}
	w.log.Info("Twitter watch list generated, subscribing to %d users.", len(l))
	return &twitter.StreamFilterParams{Follow: l, Language: []string{"en"}, FilterLevel: "none", StallWarnings: twitter.Bool(true)}, nil
}
func (w *Watcher) watch(x context.Context, g *sync.WaitGroup, c chan uint8, o chan<- *twitter.Tweet) {
	var (
		z = make(chan interface{})
		y = time.NewTicker(drop)
		s *twitter.Stream
		r <-chan interface{}
		i int8
		d bool
		l *twitter.StreamFilterParams
	)
	w.log.Info("Starting Twitter stream thread..")
	if l, w.err = w.stream(x, true, true); w.err != nil {
		w.log.Error("Error creating initial Twitter stream filter: %s!", w.err.Error())
		y.Stop()
		close(z)
		w.cancel()
		return
	}
	if l != nil {
		w.log.Trace("Twitter stream options: %+v", l)
		if s, w.err = w.twitter.Streams.Filter(l); w.err != nil {
			w.log.Error("Error creating initial Twitter stream: %s!", w.err.Error())
			y.Stop()
			close(z)
			w.cancel()
			return
		}
		r = s.Messages
	} else {
		r, s = z, nil
	}
	for g.Add(1); ; {
		select {
		case <-y.C:
			if !d {
				break
			}
			if d = false; i >= 0 {
				c <- uint8(i)
			}
			i = -1
			w.log.Debug("Drop complete, I can now accept more requests.")
		case n := <-r:
			switch t := n.(type) {
			case *twitter.Tweet:
				if t.ExtendedTweet != nil && len(t.ExtendedTweet.FullText) > 0 {
					t.FullText = t.ExtendedTweet.FullText
				}
				w.log.Trace(
					"Tweet %q received! Details [Reply? %t, Retweet? %t, Size? %d, Full? %d, Quoted? %t, User? %s, URL? https://twitter.com/%s/status/%s]",
					t.IDStr, len(t.Text) > 0 && t.Text[0] == '@', t.Retweeted || t.RetweetedStatus != nil, len(t.Text), len(t.FullText),
					len(t.QuotedStatusIDStr) > 0 || len(t.InReplyToStatusIDStr) > 0, t.User.ScreenName, t.User.ScreenName, t.IDStr,
				)
				if len(t.Text) == 0 && len(t.FullText) == 0 {
					w.log.Debug("Tweet \"twitter.com/%s/status/%s\" is empty or just an image, skipping it!", t.User.ScreenName, t.IDStr)
					return
				}
				if t.Retweeted || t.RetweetedStatus != nil {
					w.log.Debug(`Tweet "twitter.com/%s/status/%s" is a a retweet, skipping it!`, t.User.ScreenName, t.IDStr)
					break
				}
				if t.Text[0] == '@' || t.Retweeted || t.RetweetedStatus != nil {
					w.log.Debug(`Tweet "twitter.com/%s/status/%s" is a direct reply, skipping it!`, t.User.ScreenName, t.IDStr)
					break
				}
				if t.WithheldScope = ""; len(t.QuotedStatusIDStr) != 0 || len(t.InReplyToStatusIDStr) != 0 {
					w.log.Debug(`Tweet "twitter.com/%s/status/%s" is a quoted retweet skipping it!`, t.User.ScreenName, t.IDStr)
					break
				}
				o <- t
			case *twitter.Event:
			case *twitter.FriendsList:
			case *twitter.UserWithheld:
			case *twitter.DirectMessage:
			case *twitter.StatusDeletion:
			case *twitter.StatusWithheld:
			case *twitter.LocationDeletion:
			case *twitter.StreamLimit:
				w.log.Warning("Twitter stream thread received a StreamLimit message of %d!", t.Track)
			case *twitter.StallWarning:
				w.log.Warning("Twitter stream thread received a StallWarning message: %s!", t.Message)
			case *twitter.StreamDisconnect:
				w.log.Error("Twitter stream thread received a StreamDisconnect message: %s!", t.Reason)
				w.log.Info("Waiting %s before retrying...", pause.String())
				time.Sleep(pause)
				d = false // Remove any backoffs beforehand, since they don't matter,
				c <- 0
				w.log.Info("Wait complete, retrying!")
			case *url.Error:
				w.log.Error("Twitter stream thread received an error: %s!", t.Error())
				w.log.Info("Waiting %s before retrying...", pause.String())
				time.Sleep(pause)
				d = false // Remove any backoffs beforehand, since they don't matter,
				c <- 0
				w.log.Info("Wait complete, retrying!")
			default:
				if t != nil {
					w.log.Warning("Twitter stream thread received an unrecognized message (%T): %s\n", t, t)
				}
			}
		case a := <-c:
			if d {
				if int8(a) > i {
					i = int8(a)
				}
				w.log.Debug("Ignoring dropped request! (%d, next %d)", a, i)
				break
			}
			if w.log.Info("Attempting to reload Twitter stream.."); s != nil {
				s.Stop()
				time.Sleep(time.Millisecond * 150)
			}
			if l, w.err = w.stream(x, a > 0, a > 1); w.err != nil {
				w.log.Error("Error creating Twitter stream filter: %s!", w.err.Error())
				y.Stop()
				close(z)
				w.cancel()
				return
			}
			if l != nil {
				w.log.Trace("Twitter stream options: %+v", l)
				if s, w.err = w.twitter.Streams.Filter(l); w.err != nil {
					w.log.Error("Error creating Twitter stream: %s!", w.err.Error())
					y.Stop()
					close(z)
					w.cancel()
					g.Done()
					return
				}
				r = s.Messages
				w.log.Debug("Twitter stream reloaded successfully.")
			} else {
				r, s = z, nil
			}
			w.log.Debug("Dropping all entries on the floor until next tick!")
			d, i = true, -1
		case <-x.Done():
			w.log.Info("Stopping Twitter stream thread.")
			if y.Stop(); s != nil {
				s.Stop()
			}
			close(z)
			g.Done()
			return
		}
	}
}
func (w *Watcher) mentions(x context.Context, g *sync.WaitGroup, i *notifier, o chan<- *twitter.Tweet) {
	if i == nil {
		w.log.Info("Mentions config is empty, not starting mentions thread.")
		return
	}
	var s *twitter.Stream
	if i.client != nil {
		w.log.Debug("Mention stream using secondary stream mode..")
		s, w.err = i.client.Streams.Filter(&twitter.StreamFilterParams{
			Track:         i.keywords,
			Language:      []string{"en"},
			FilterLevel:   "none",
			StallWarnings: twitter.Bool(true),
		})
	} else {
		w.log.Debug("Mention stream using User mode..")
		s, w.err = w.twitter.Streams.User(&twitter.StreamUserParams{
			With:          "followings",
			Track:         i.keywords,
			Replies:       "all",
			Language:      []string{"en"},
			FilterLevel:   "none",
			StallWarnings: twitter.Bool(true),
		})
	}
	if w.err != nil {
		w.log.Error("Error creating initial Twitter mention stream: %s!", w.err.Error())
		w.cancel()
		return
	}
	w.log.Info("Starting Twitter mentions thread..")
	for g.Add(1); ; {
		select {
		case n := <-s.Messages:
			switch t := n.(type) {
			case *twitter.Tweet:
				if t.ExtendedTweet != nil && len(t.ExtendedTweet.FullText) > 0 {
					t.FullText = t.ExtendedTweet.FullText
				}
				w.log.Trace(
					"Tweet %q received! Details [Reply? %t, Retweet? %t, Size? %d, Full? %d, Quoted? %t, User? %s, URL? https://twitter.com/%s/status/%s]",
					t.IDStr, len(t.Text) > 0 && t.Text[0] == '@', t.Retweeted || t.RetweetedStatus != nil, len(t.Text), len(t.FullText),
					len(t.QuotedStatusIDStr) > 0 || len(t.InReplyToStatusIDStr) > 0, t.User.ScreenName, t.User.ScreenName, t.IDStr,
				)
				w.log.Debug(`Received mention "twitter.com/%s/status/%s", sending to %d!`, t.User.ScreenName, t.IDStr, i.chat)
				t.WithheldScope = "mention"
				o <- t
			case *twitter.Event:
			case *twitter.FriendsList:
			case *twitter.UserWithheld:
			case *twitter.DirectMessage:
			case *twitter.StatusDeletion:
			case *twitter.StatusWithheld:
			case *twitter.LocationDeletion:
			case *twitter.StreamLimit:
				w.log.Warning("Twitter mention thread received a StreamLimit message of %d!", t.Track)
			case *twitter.StallWarning:
				w.log.Warning("Twitter mention thread received a StallWarning message: %s!", t.Message)
			case *twitter.StreamDisconnect:
				w.log.Error("Twitter mention thread received a StreamDisconnect message: %s!", t.Reason)
				// w.err = &errval{s: "received StreamDisconnect: " + t.Reason}
				// I don't think we should close on mention thread errors.
				s.Stop()
				g.Done()
				return
			case *url.Error:
				w.log.Error("Twitter mention thread received an error: %s!", t.Error())
				// w.err = t.Err
				// same as above
				s.Stop()
				g.Done()
				return
			default:
				if t != nil {
					w.log.Warning("Twitter mention thread received an unrecognized message (%T): %s\n", t, t)
				}
			}
		case <-x.Done():
			w.log.Info("Stopping Twitter mention thread.")
			s.Stop()
			g.Done()
			return
		}
	}
}
