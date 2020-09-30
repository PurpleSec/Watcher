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
	"database/sql"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/PurpleSec/logx"
	"github.com/PurpleSec/mapper"
	"github.com/dghubble/go-twitter/twitter"
	"github.com/dghubble/oauth1"

	telegram "github.com/go-telegram-bot-api/telegram-bot-api"
)

// Watcher is a struct that is used to manage the threads and proceses used to control and operate
// the Telegram Watcher bot service.
type Watcher struct {
	wg          sync.WaitGroup
	sql         *mapper.Map
	bot         *telegram.BotAPI
	log         logx.Log
	recv        <-chan telegram.Update
	send        chan message
	cancel      context.CancelFunc
	reload      chan interface{}
	tweets      chan *twitter.Tweet
	update      *time.Timer
	resolve     chan interface{}
	twitter     *twitter.Client
	allowed     []string
	blocked     []string
	timeouts    *timeouts
	backoffTime time.Duration
	resolveTime time.Duration
}
type resolve struct {
	id   int64
	tid  int64
	name string
}
type message struct {
	msg   telegram.MessageConfig
	tries uint8
}
type errorval struct {
	e error
	s string
}

func (w *Watcher) stop() error {
	w.cancel()
	w.wg.Wait()
	close(w.send)
	close(w.reload)
	close(w.tweets)
	close(w.resolve)
	return w.sql.Close()
}
func (e errorval) Error() string {
	if e.e == nil {
		return e.s
	}
	return e.s + ": " + e.e.Error()
}
func (e errorval) Unwrap() error {
	return e.e
}
func matchLower(s, m string) bool {
	if len(s) != len(m) {
		return false
	}
	for i := range s {
		switch {
		case s[i] == m[i]:
		case m[i] > 96 && s[i]+32 == m[i]:
		case s[i] > 96 && m[i]+32 == s[i]:
		default:
			return false
		}
	}
	return true
}
func validHandle(s string) (string, bool) {
	v := strings.TrimSpace(s)
	if len(v) == 0 || v[0] != '@' {
		return v, false
	}
	v = v[1:]
	for i := range v {
		switch {
		case v[i] == '_':
			continue
		case v[i] < 48 || v[i] > 122:
			return v, false
		case v[i] > 57 && v[i] < 65:
			return v, false
		case v[i] > 90 && v[i] < 96:
			return v, false
		}
	}
	return v, true
}
func isAllowed(n string, a, d []string) bool {
	if len(d) == 0 && len(a) == 0 {
		return true
	}
	if len(d) > 0 {
		for i := range d {
			if matchLower(n, d[i]) {
				return false
			}
		}
	}
	if len(a) == 0 {
		return true
	}
	for i := range a {
		if matchLower(n, a[i]) {
			return true
		}
	}
	return false
}
func splitParams(s string) ([]string, string) {
	var (
		v bool
		a = strings.Split(s, ",")
	)
	for q := range a {
		if a[q], v = validHandle(a[q]); !v {
			return nil, `The username "` + a[q] + badname
		}
	}
	return a, ""
}

// Start will start the main Watcher process and all associated threads. This function will block until
// the context is cancled or an interrupt signal is received.
func (w *Watcher) Start(ctx context.Context) error {
	var (
		c = make(chan os.Signal, 1)
		x context.Context
	)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	x, w.cancel = context.WithCancel(ctx)
	w.log.Info("Twitter Watcher Telegram Bot Started, spinning up threads...")
	go w.threadSubscribe(x)
	go w.threadTelegramSend(x)
	go w.threadTelgramReceive(x)
	select {
	case <-c:
	case <-x.Done():
	}
	w.log.Info("Stop signal received, closing resources and stopping threads...")
	return w.stop()
}

// NewWatcher returns a new watcher instance based on the passed configuration. This function will preform any
// setup steps needed to start the Watcher. Once complete, use the 'Start' function to actually start the Watcher.
// The supplied boolean value can be used to clean and prepare the database before running.
func NewWatcher(c Config, cls bool) (*Watcher, error) {
	if err := c.check(); err != nil {
		return nil, err
	}
	l := logx.Multiple(logx.Console(logx.Level(c.Log.Level)))
	if len(c.Log.File) > 0 {
		f, err := logx.File(c.Log.File, logx.Append, logx.Level(c.Log.Level))
		if err != nil {
			return nil, &errorval{s: `error setting up log file "` + c.Log.File + `"`, e: err}
		}
		l.Add(f)
	}
	h := oauth1.NewConfig(c.Twitter.ConsumerKey, c.Twitter.ConsumerSecret).Client(
		context.Background(), oauth1.NewToken(c.Twitter.AccessKey, c.Twitter.AccessSecret),
	)
	h.Timeout = c.Timeouts.Web
	t := twitter.NewClient(h)
	if _, _, err := t.Accounts.VerifyCredentials(nil); err != nil {
		return nil, &errorval{s: "login to Twitter failed", e: err}
	}
	b, err := telegram.NewBotAPI(c.Telegram)
	if err != nil {
		return nil, &errorval{s: "error setting up Telegram", e: err}
	}
	r, err := b.GetUpdatesChan(telegram.UpdateConfig{Timeout: int(c.Timeouts.Telegram / time.Second)})
	if err != nil {
		return nil, &errorval{s: "error setting up Telegram receiver", e: err}
	}
	d, err := sql.Open(
		"mysql",
		c.Database.Username+":"+c.Database.Password+"@"+c.Database.Server+"/"+c.Database.Name+"?multiStatements=true&interpolateParams=true",
	)
	if err != nil {
		return nil, &errorval{s: "error setting up database connection", e: err}
	}
	if d.SetConnMaxLifetime(c.Timeouts.Database); cls {
		for i := range cleanStatements {
			if _, err := d.Exec(cleanStatements[i]); err != nil {
				d.Close()
				return nil, &errorval{s: "error cleaning up database schema", e: err}
			}
		}
	}
	for i := range setupStatements {
		if _, err := d.Exec(setupStatements[i]); err != nil {
			d.Close()
			return nil, &errorval{s: "error setting up database schema", e: err}
		}
	}
	m := &mapper.Map{Database: d}
	if err = m.Extend(queryStatements); err != nil {
		m.Close()
		return nil, &errorval{s: "error setting up database schema", e: err}
	}
	w := &Watcher{
		sql:         m,
		bot:         b,
		log:         l,
		recv:        r,
		send:        make(chan message, bufferSize),
		reload:      make(chan interface{}, bufferSmallSize),
		tweets:      make(chan *twitter.Tweet, bufferSize),
		update:      time.NewTimer(c.Timeouts.Resolve),
		resolve:     make(chan interface{}, bufferSmallSize),
		twitter:     t,
		allowed:     c.Allowed,
		blocked:     c.Blocked,
		backoffTime: c.Timeouts.Backoff,
		resolveTime: c.Timeouts.Resolve,
	}
	return w, nil
}
