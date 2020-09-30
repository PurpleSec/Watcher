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
	"strings"
	"time"

	telegram "github.com/go-telegram-bot-api/telegram-bot-api"
)

const denied = `I'm sorry but my permissions do not allow you to use this service.`
const errmsg = `I'm sorry, There seems to have been an error trying to process your request
Please try again later.`
const invalid = `I'm sorry I don't understand that command.

Please use a command from the following list:
/list
/add <@username1,@usernameN,..>
/remove <@username1,@usernameN,..|clear|all>`
const badname = `" is not a valid Twitter username!

Twitter names must start with "@" and contain no special characters or spaces.`

func (w *Watcher) threadTelegramSend(x context.Context) {
	w.wg.Add(1)
	for {
		select {
		case <-x.Done():
			w.log.Debug("Stopping Telegram message handler thread...")
			w.wg.Done()
			return
		case n := <-w.send:
			if _, err := w.bot.Send(n.msg); err != nil {
				w.log.Warning(`Error sending Telegram message to "%d": %s!`, n.msg.ChatID, err.Error())
				if n.tries <= 1 {
					w.log.Error(`Removing Telegram message to "%d": Send failed too many times!`, n.msg.ChatID)
				} else {
					w.log.Info("Sleeping for %q to Telegram prevent rate-limiting!", w.timeouts.Backoff.String())
					time.Sleep(w.timeouts.Backoff)
					n.tries = n.tries - 1
					w.send <- n
				}
			}
		}
	}
}
func (w *Watcher) threadTelgramReceive(x context.Context) {
	w.wg.Add(1)
	for {
		select {
		case <-x.Done():
			w.log.Debug("Stopping Telegram message receiver tread...")
			w.wg.Done()
			return
		case n := <-w.recv:
			w.log.Trace("Received Telegram message from %s: %d...", n.Message.Chat.UserName, n.Message.Chat.ID)
			w.send <- message{
				tries: 3,
				msg: telegram.NewMessage(
					n.Message.Chat.ID, w.handleMessage(x, n.Message.Chat.ID, n.Message.Chat.UserName, n.Message.Text),
				),
			}
		case n := <-w.tweets:
			w.log.Trace("Received Tweet from %s: %s...", n.User.ScreenName, n.User.IDStr)
			w.handleTweet(
				x, n.User.ID,
				"New Tweet from @"+n.User.ScreenName+"!\n\n"+n.Text+
					"\n\nhttps://twitter.com/"+n.User.ScreenName+"/status/"+n.IDStr,
			)
		}
	}
}
func (w *Watcher) actionList(x context.Context, i int64) string {
	r, err := w.sql.QueryContext(x, "get", i)
	if err != nil {
		w.log.Error("Error during Telegram action execution (DB Get): %s!", err.Error())
		return errmsg
	}
	var (
		c int
		s string
	)
	o := "I am currently following these users:\n"
	for r.Next() {
		if err := r.Scan(&s); err != nil {
			w.log.Error("Error during Telegram action execution (DB Scan Entry): %s!", err.Error())
			continue
		}
		if len(s) == 0 {
			continue
		}
		o += "- @" + s + "\n"
		c++
	}
	if r.Close(); c == 0 {
		return "There are currently no users that I am following for you."
	}
	return o
}
func (w *Watcher) handleTweet(x context.Context, i int64, s string) {
	r, err := w.sql.QueryContext(x, "get_notify", i)
	if err != nil {
		w.log.Error("Error during Tweet handler processing (DB): %s!", err.Error())
		return
	}
	var c int64
	for r.Next() {
		if err := r.Scan(&c); err != nil {
			w.log.Error("Error during Tweet handler processing (DB Scan Entry): %s!", err.Error())
			continue
		}
		if c == 0 {
			continue
		}
		w.log.Trace("Sending Telegram update for Tweet %d to %d...", i, c)
		w.send <- message{tries: 3, msg: telegram.NewMessage(c, s)}
	}
	r.Close()
}
func (w *Watcher) handleMessage(x context.Context, i int64, u, s string) string {
	if !isAllowed(u, w.allowed, w.blocked) {
		return denied
	}
	if len(s) < 5 || s[0] != '/' {
		return invalid
	}
	d := strings.IndexByte(s, ' ')
	if d < 4 && !(s[1] == 'l' || s[1] == 'L') {
		return invalid
	}
	if d == -1 {
		d = len(s)
	}
	switch strings.ToLower(s[1:d]) {
	case "add", "list", "remove":
	default:
		return invalid
	}
	if s[1] == 'l' || s[1] == 'L' {
		return w.actionList(x, i)
	}
	return w.actionUpdate(x, i, s[d+1:], s[1] == 'a' || s[1] == 'A')
}
func (w *Watcher) actionUpdate(x context.Context, i int64, s string, a bool) string {
	if p := strings.IndexByte(s, ','); p == -1 && !a {
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "all", "clear":
			if _, err := w.sql.ExecContext(x, "del_all", i); err != nil {
				w.log.Error("Error during Telegram action execution (DB Delete All): %s!", err.Error())
				return errmsg
			}
			w.reload <- wake
			return "Awesome! I have cleared your following list!"
		}
	}
	c, e := splitParams(s)
	if len(e) > 0 {
		return e
	}
	if a {
		var (
			n bool
			m int64
		)
		for p := range c {
			r, err := w.sql.QueryContext(x, "add", i, c[p])
			if err != nil {
				w.log.Error("Error during Telegram action execution (DB Update List): %s!", err.Error())
				return errmsg
			}
			for !n && r.Next() {
				if n {
					break
				}
				if r.Scan(&m); m == 0 {
					n = true
					break
				}
			}
			r.Close()
		}
		if n {
			w.resolve <- wake
		}
		return "Awesome! Your following list was updated!"
	}
	for p := range c {
		if _, err := w.sql.ExecContext(x, "del", i, c[p]); err != nil {
			w.log.Error("Error during Telegram action execution (DB Delete List): %s!", err.Error())
			return errmsg
		}
	}
	w.reload <- wake
	return "Awesome! Your following list was updated!"
}
