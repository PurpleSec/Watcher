// Copyright 2021 - 2023 PurpleSec Team
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
	"encoding/json"
	"html"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	twitter "github.com/g8rswimmer/go-twitter/v2"
)

const (
	drop  = time.Minute
	pause = time.Second * 5
)

type token struct {
	_     [0]func()
	Type  string `json:"token_type"`
	Token string `json:"access_token"`
}
type mapping struct {
	_       [0]func()
	New     string
	Name    string
	Twitter string
	ID      int64
	TID     int64
	Update  bool
}

func parseTweetText(v *twitter.TweetObj, t *twitter.TweetRaw) string {
	s := html.UnescapeString(v.Text)
	for i := range v.Entities.URLs {
		s = strings.ReplaceAll(s, v.Entities.URLs[i].URL, v.Entities.URLs[i].ExpandedURL)
	}
	return s
}
func (w *Watcher) resolve(x context.Context, t *twitter.Client, a bool) {
	w.log.Info("Starting Twitter ID mapping resolve task..")
	r, err := w.sql.QueryContext(x, "get_all")
	if err != nil {
		w.log.Error("Error getting Twitter mappings from database: %s!", err.Error())
		return
	}
	var (
		l       = make([]*mapping, 0, 64)
		s       string
		c, m, u int64
	)
	for r.Next() {
		if err = r.Scan(&c, &m, &s, &u); err != nil {
			w.log.Error("Error scanning data into Twitter mappings from database: %s!", err.Error())
			continue
		}
		if !a && u != 0 || len(s) == 0 {
			continue
		}
		if cap(l) < int(c) {
			l = append(make([]*mapping, 0, int(c)-cap(l)), l...)
		}
		l = append(l, &mapping{ID: m, TID: u, Name: s, Twitter: strconv.FormatInt(u, 10)})
	}
	if r.Close(); len(l) == 0 {
		w.log.Debug("Twitter resolve mapping is empty, not attempting to resolve..")
		return
	}
	var (
		i    = make(map[string]string, len(l))
		p, n []string
	)
	w.log.Debug("Twitter mapping generated, attempting to resolve to %d user IDs..", len(l))
	for q, z := 0, 0; q < len(l); q += 50 {
		if z = q + 50; z > len(l) {
			z = len(l)
		}
		n = make([]string, 0, z-q)
		for _, v := range l[q:z] {
			n = append(n, v.Name)
		}
		for _, v := range l[q:z] {
			p = append(p, v.Twitter)
		}
		r, err := t.UserNameLookup(x, n, twitter.UserLookupOpts{UserFields: []twitter.UserField{twitter.UserFieldID, twitter.UserFieldUserName}})
		if err != nil {
			w.log.Error("Error retrieving data about Twitter mappings from Twitter: %s!", err.Error())
			continue
		}
		for v := range r.Raw.Users {
			if _, ok := i[r.Raw.Users[v].ID]; ok {
				w.log.Warning(`Duplicate ID value "%s" detected with username "%s"!`, r.Raw.Users[v].ID, r.Raw.Users[v].UserName)
			}
			i[r.Raw.Users[v].ID] = r.Raw.Users[v].UserName
		}
		r, err = t.UserLookup(x, p, twitter.UserLookupOpts{UserFields: []twitter.UserField{twitter.UserFieldID, twitter.UserFieldName, twitter.UserFieldUserName}})
		if err != nil {
			w.log.Error("Error retrieving data about Twitter mappings from Twitter: %s!", err.Error())
			continue
		}
		for v := range r.Raw.Users {
			// We don't need to check for duplicates here.
			i[r.Raw.Users[v].ID] = r.Raw.Users[v].UserName
		}
	}
	for k, v := range i {
		for x := range l {
			if len(l[x].Twitter) > 0 && l[x].Twitter == k && !stringLowMatch(v, l[x].Name) {
				if l[x].New, l[x].Update = v, true; l[x].TID == 0 {
					l[x].TID, _ = strconv.ParseInt(k, 10, 64)
				}
				w.log.Warning(`Found new name for ID "%s": %s => %s!`, k, l[x].Name, l[x].New)
				continue
			}
			if !stringLowMatch(v, l[x].Name) {
				continue
			}
			if l[x].TID == 0 || len(l[x].Twitter) == 0 {
				l[x].TID, _ = strconv.ParseInt(k, 10, 64)
				l[x].Update, l[x].Twitter = true, k
			}
			w.log.Trace(`Twitter username %s (db: %s) was resolved to "%s".`, v, l[x].Name, k)
		}
	}
	for x := range l {
		if !l[x].Update {
			continue
		}
		if len(l[x].New) > 0 {
			_, err = w.sql.Exec("set", l[x].ID, uint64(l[x].TID), l[x].New)
		} else {
			_, err = w.sql.Exec("set", l[x].ID, uint64(l[x].TID), l[x].Name)
		}
		if err != nil {
			w.log.Error("Error updating Twitter mappings in the database: %s!", err.Error())
			continue
		}
	}
	w.log.Debug("Completed Twitter ID mapping resolve task!")
}
func (w *Watcher) setupAuth(x context.Context, t *twitter.Client) error {
	r, _ := http.NewRequestWithContext(x, "POST", "https://api.twitter.com/oauth2/token", strings.NewReader("grant_type=client_credentials"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	r.SetBasicAuth(w.ck, w.cs)
	o, err := t.Client.Do(r)
	if err != nil {
		return err
	}
	var i token
	err = json.NewDecoder(o.Body).Decode(&i)
	if o.Body.Close(); err != nil {
		return err
	}
	w.auth = i.Token
	return nil
}
func (w *Watcher) watch(x context.Context, g *sync.WaitGroup, c chan uint8, o chan<- *twitter.TweetObj) {
	t := &twitter.Client{
		Host: "https://api.twitter.com",
		Client: &http.Client{
			Transport: &http.Transport{
				DialContext:           (&net.Dialer{Timeout: time.Second * 10, KeepAlive: time.Second * 30}).DialContext,
				MaxIdleConns:          256,
				IdleConnTimeout:       time.Second * 60,
				DisableKeepAlives:     false,
				ForceAttemptHTTP2:     true,
				TLSHandshakeTimeout:   time.Second * 10,
				ExpectContinueTimeout: time.Second * 10,
				ResponseHeaderTimeout: time.Second * 10,
			},
		},
		Authorizer: w,
	}
	if w.err = w.setupAuth(x, t); w.err != nil {
		w.log.Error("Error logging in using OAUTHv2: %s!", w.err.Error())
		w.cancel()
		return
	}
	var (
		z = make(chan *twitter.TweetMessage)
		y = time.NewTicker(drop)
		r <-chan *twitter.TweetMessage
		m <-chan map[twitter.SystemMessageType]twitter.SystemMessage
		e <-chan *twitter.DisconnectionError
		i int8
		d bool
	)
	w.log.Info("Starting Twitter stream thread..")
	s, k, err := w.stream(x, t, true, true)
	if err != nil {
		w.log.Error("Error creating initial Twitter stream: %s!", err.Error())
		w.err = err
		goto done
	}
	if s != nil {
		r, m, e = s.Tweets(), s.SystemMessages(), s.DisconnectionError()
	} else {
		r, m, e = z, nil, nil
	}
	for g.Add(1); ; {
		select {
		case <-e:
			w.log.Error("Twitter stream thread received a StreamDisconnect message!")
			w.log.Info("Waiting %s before retrying..", pause.String())
			if time.Sleep(pause); len(c) == 0 {
				d = false // Remove any backoffs beforehand, since they don't matter,
				c <- 0
			}
			w.log.Debug("Wait complete, retrying!")
		case <-y.C:
			if !d {
				break
			}
			if d = false; i >= 0 {
				c <- uint8(i)
			}
			i = -1
			w.log.Debug("Drop complete, I can now accept more requests.")
		case o := <-m:
			if len(o) == 0 {
				break
			}
			for k, v := range o {
				w.log.Warning("Twitter stream thread received a %s message: %s!", k, v)
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
				if len(k) > 0 {
					t.TweetSearchStreamDeleteRuleByID(x, k, false)
				}
				s.Close()
				time.Sleep(time.Millisecond * 150)
			}
			if s, k, w.err = w.stream(x, t, a > 0, a > 1); w.err != nil {
				w.log.Error("Error re-creating Twitter stream: %s!", w.err.Error())
				goto done
			}
			if s != nil {
				r, m, e = s.Tweets(), s.SystemMessages(), s.DisconnectionError()
			} else {
				r, m, e = z, nil, nil
			}
			w.log.Debug("Dropping all entries on the floor until next tick!")
			d, i = true, -1
		case n := <-r:
			if len(n.Raw.Tweets) == 0 {
				break
			}
			v := n.Raw.Tweets[0] // There isn't more than one Tweet in here mostly.
			if a := len(n.Raw.Tweets); a > 1 {
				w.log.Warning("Tweet container returned %d Tweets instead of just one!", a)
			}
			if len(n.Raw.Includes.Users) > 0 { // First user is usually the author.
				v.Source = n.Raw.Includes.Users[0].UserName
			}
			w.log.Trace(
				`Tweet "%s" received! Details [Reply? %t, Retweet/Quote? %t, Size? %d, User? %s, URL? https://twitter.com/%s/status/%s]`,
				v.ID, (len(v.Text) > 0 && v.Text[0] == '@') || len(v.InReplyToUserID) > 0, len(v.ReferencedTweets) > 0, len(v.Text), v.Source,
				v.Source, v.ID,
			)
			if len(v.Text) == 0 {
				w.log.Debug(`Tweet "twitter.com/%s/status/%s" is empty or just an image, skipping it!`, v.Source, v.ID)
				continue
			}
			if v.Text[0] == '@' || len(v.InReplyToUserID) > 0 || len(v.ReferencedTweets) > 0 {
				w.log.Debug(`Tweet "twitter.com/%s/status/%s" is a direct reply or retweet, skipping it!`, v.Source, v.ID)
				continue
			}
			v.Text = parseTweetText(v, n.Raw)
			o <- v
		case <-x.Done():
			w.log.Info("Stopping Twitter stream thread.")
			goto done
		}
	}
done:
	y.Stop()
	if close(z); s != nil {
		if len(k) > 0 {
			t.TweetSearchStreamDeleteRuleByID(x, k, false)
		}
		s.Close()
	}
	w.log.Info("Stopped Twitter stream thread.")
	w.cancel()
	g.Done()
}
func (w *Watcher) stream(x context.Context, t *twitter.Client, f bool, a bool) (*twitter.TweetStream, []twitter.TweetSearchStreamRuleID, error) {
	if f {
		w.resolve(x, t, a)
	}
	r, err := w.sql.QueryContext(x, "get_list")
	if err != nil {
		w.log.Error("Error getting Twitter list from database: %s!", err.Error())
		return nil, nil, err
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
		l = append(l, "from:"+strconv.FormatInt(s, 10))
	}
	if r.Close(); err != nil {
		return nil, nil, err
	}
	if len(l) == 0 {
		w.log.Info("Twitter watch list is empty, not starting Twitter stream..")
		return nil, nil, nil
	}
	w.log.Info("Twitter watch list generated, subscribing to %d users.", len(l))
	var (
		k = make([]twitter.TweetSearchStreamRule, 0, 4)
		b = builders.Get().(*strings.Builder)
	)
	for i := range l {
		if len(l[i])+46+b.Len() >= 510 {
			k = append(k, twitter.TweetSearchStreamRule{Value: " (" + b.String() + ") -is:retweet -is:quote -is:reply lang:en"})
			b.Reset()
		}
		if b.Len() > 0 {
			b.WriteString(" OR ")
		}
		b.WriteString(l[i])
	}
	if b.Len() > 0 {
		k = append(k, twitter.TweetSearchStreamRule{Value: "(" + b.String() + ") -is:retweet -is:quote -is:reply lang:en"})
	}
	b.Reset()
	builders.Put(b)
	y, err := t.TweetSearchStreamAddRule(x, k, false)
	if err != nil {
		return nil, nil, err
	}
	v := make([]twitter.TweetSearchStreamRuleID, len(y.Rules))
	for i := range y.Rules {
		v[i] = y.Rules[i].ID
	}
	o, err := t.TweetSearchStream(x, twitter.TweetSearchStreamOpts{
		Expansions: []twitter.Expansion{
			twitter.ExpansionAuthorID,
		},
		UserFields: []twitter.UserField{
			twitter.UserFieldID,
			twitter.UserFieldUserName,
		},
		TweetFields: []twitter.TweetField{
			twitter.TweetFieldID,
			twitter.TweetFieldText,
			twitter.TweetFieldAuthorID,
			twitter.TweetFieldInReplyToUserID,
			twitter.TweetFieldReferencedTweets,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	return o, v, err
}
