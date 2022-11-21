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
	"database/sql"
	"strings"
	"sync"
	"time"

	"github.com/dghubble/go-twitter/twitter"

	telegram "github.com/go-telegram-bot-api/telegram-bot-api"
)

var builders = sync.Pool{
	New: func() interface{} {
		return new(strings.Builder)
	},
}

var confirm struct{}

func (w *Watcher) clear(x context.Context, i int64) bool {
	if _, err := w.sql.ExecContext(x, "del_all", i); err != nil {
		w.log.Error("Error clearing Twitter subscriptions from database: %s!", err.Error())
		return false
	}
	return true
}
func (w *Watcher) list(x context.Context, i int64) string {
	r, err := w.sql.QueryContext(x, "list", i)
	if err != nil {
		w.log.Error("Error getting Twitter subscription list from database: %s!", err.Error())
		return errmsg
	}
	var (
		c int
		t int64
		s string
		k sql.NullString
		b = builders.Get().(*strings.Builder)
	)
	for b.WriteString("I am currently following these users:\n"); r.Next(); {
		if err := r.Scan(&s, &t, &k); err != nil {
			w.log.Error("Error scanning data into Twitter subscriptions list from database: %s!", err.Error())
			continue
		}
		if len(s) == 0 {
			continue
		}
		b.WriteString("- @" + s)
		if t == 0 {
			b.WriteString(" (Might not be valid!)")
		}
		if k.Valid && len(k.String) > 0 {
			b.WriteString("\n  [" + k.String + "]")
		}
		b.WriteByte('\n')
		c++
	}
	r.Close()
	s = b.String()
	b.Reset()
	if builders.Put(b); c == 0 {
		return "There are currently no users that I am following for you."
	}
	return s
}
func (w *Watcher) tweet(x context.Context, m chan<- message, t *twitter.Tweet) {
	r, err := w.sql.QueryContext(x, "notify", t.User.ID)
	if err != nil {
		w.log.Error("Error getting Twitter subscriptions from database: %s!", err.Error())
		return
	}
	b := t.FullText
	if len(b) == 0 {
		b = t.Text
	}
	var (
		c int64
		k sql.NullString
		v = strings.ToLower(b)
		s = "Tweet from @" + t.User.ScreenName + "!\n\n" + b + "\n\nhttps://twitter.com/" + t.User.ScreenName + "/status/" + t.IDStr
	)
	for r.Next() {
		if err := r.Scan(&c, &k); err != nil {
			w.log.Error("Error scanning data into Twitter subscriptions from database: %s!", err.Error())
			continue
		}
		if c == 0 {
			continue
		}
		w.log.Trace(`Received Tweet "twitter.com/%s/status/%s", match on Chat %d (Keywords: %t).`, t.User.ScreenName, t.IDStr, c, k.Valid)
		if !k.Valid || (k.Valid && stringSplitContainsNLA(v, k.String)) {
			w.log.Debug(`Sending Telegram update for Tweet "twitter.com/%s/status/%s" to chat %d..`, t.User.ScreenName, t.IDStr, c)
			m <- message{tries: 2, msg: telegram.NewMessage(c, s)}
			continue
		}
		w.log.Trace(`Skipping Telegram update for Tweet "twitter.com/%s/status/%s" to %d as it does not match keywords!`, t.User.ScreenName, t.IDStr, c)
	}
	r.Close()
}
func (w *Watcher) message(x context.Context, n *telegram.Message, c chan<- uint8) string {
	if len(n.From.UserName) == 0 || !canUseACL(n.From.UserName, w.allowed, w.blocked) {
		return `I'm sorry but my permissions do not allow you to use this service.`
	}
	_, ok := w.confirm[n.Chat.ID]
	if delete(w.confirm, n.Chat.ID); ok && stringLowMatch(n.Text, "confirm") {
		if r := w.clear(x, n.Chat.ID); !r {
			return errmsg
		}
		c <- 0
		return "Awesome! I have cleared your following list!"
	}
	if len(n.Text) < 5 || n.Text[0] != '/' {
		return invalid
	}
	d := strings.IndexByte(n.Text, ' ')
	if d < 4 && !(n.Text[1] == 'l' || n.Text[1] == 'L' || n.Text[1] == 'c' || n.Text[1] == 'C') {
		return invalid
	}
	if d == -1 {
		d = len(n.Text)
	}
	switch strings.ToLower(n.Text[1:d]) {
	case "clear":
		w.confirm[n.Chat.ID] = confirm
		return `Please reply with "confirm" in order to clear your list.`
	case "add", "list", "remove":
	default:
		return invalid
	}
	if n.Text[1] == 'l' || n.Text[1] == 'L' {
		return w.list(x, n.Chat.ID)
	}
	return w.action(x, n.Chat.ID, n.Text[d+1:], n.Text[1] == 'a' || n.Text[1] == 'A', c)
}
func (w *Watcher) action(x context.Context, i int64, s string, a bool, c chan<- uint8) string {
	if p := strings.IndexByte(s, ','); p == -1 && !a {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "all", "clear":
			w.confirm[i] = confirm
			return `Please reply with "confirm" in order to clear your list.`
		}
	}
	n, k, msg := split(strings.TrimSpace(s))
	if len(msg) > 0 {
		return msg
	}
	if len(k) > 256 {
		w.log.Warning("User %d: Invalid keyword size specified %d, must be less than 256!", i, len(k))
		return `I'm sorry, but keyword lists must be under 256 characters!`
	}
	if !a {
		for p := range n {
			if _, err := w.sql.ExecContext(x, "del", i, n[p]); err != nil {
				w.log.Error("Error deleting Twitter subscription entry from database: %s!", err.Error())
				return errmsg
			}
		}
		c <- 0
		return "Awesome! Your following list was updated!"
	}
	var (
		e = sql.NullString{Valid: len(k) > 0, String: k}
		u bool
		m int64
	)
	for p := range n {
		r, err := w.sql.QueryContext(x, "add", i, n[p], e)
		if err != nil {
			w.log.Error("Error adding Twitter subscription entry to database: %s!", err.Error())
			return errmsg
		}
		for !u && r.Next() {
			if u {
				break
			}
			if r.Scan(&m); m == 0 {
				u = true
				break
			}
		}
		r.Close()
	}
	if u {
		c <- 1
	} else {
		c <- 0
	}
	return "Awesome! Your following list was updated!"
}
func (w *Watcher) send(x context.Context, g *sync.WaitGroup, m chan message, t <-chan *twitter.Tweet) {
	w.log.Info("Starting Telegram sender thread..")
	for g.Add(1); ; {
		select {
		case n := <-t:
			w.tweet(x, m, n)
		case n := <-m:
			_, err := w.bot.Send(n.msg)
			if err == nil {
				break
			}
			w.log.Warning(`Error sending Telegram message to "%d": %s!`, n.msg.ChatID, err.Error())
			if n.tries <= 1 {
				w.log.Error(`Removing Telegram message to "%d": Send failed too many times!`, n.msg.ChatID)
				break
			}
			n.tries = n.tries - 1
			w.log.Debug("Sleeping for %s to Telegram prevent rate-limiting!", w.backoff.String())
			time.Sleep(w.backoff)
			m <- n
		case <-x.Done():
			w.log.Info("Stopping Telegram sender thread.")
			g.Done()
			return
		}
	}
}
func (w *Watcher) receive(x context.Context, g *sync.WaitGroup, m chan<- message, r <-chan telegram.Update, c chan<- uint8) {
	w.log.Info("Starting Telegram receiver thread..")
	for g.Add(1); ; {
		select {
		case n := <-r:
			if n.Message == nil || n.Message.Chat == nil || len(n.Message.Text) == 0 {
				break
			}
			w.log.Trace("Received Telegram message from %s (%d).", n.Message.From.String(), n.Message.Chat.ID)
			m <- message{tries: 2, msg: telegram.NewMessage(n.Message.Chat.ID, w.message(x, n.Message, c))}
		case <-x.Done():
			w.log.Info("Stopping Telegram receiver thread.")
			g.Done()
			return
		}
	}
}
