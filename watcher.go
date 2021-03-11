// Copyright 2021 PurpleSec Team
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
	"encoding/json"
	"io/ioutil"
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
	log      logx.Log
	err      error
	sql      *mapper.Map
	bot      *telegram.BotAPI
	tick     *time.Ticker
	cancel   context.CancelFunc
	twitter  *twitter.Client
	confirm  map[int64]struct{}
	notifier *notifier
	allowed  []string
	blocked  []string
	backoff  time.Duration
}
type message struct {
	msg   telegram.MessageConfig
	tries uint8
}
type notifier struct {
	client   *twitter.Client
	keywords []string
	chat     int64
}

// Run will start the main Watcher process and all associated threads. This function will block until an
// interrupt signal is received. This function returns any errors that occur during shutdown.
func (w *Watcher) Run() error {
	telegram.SetLogger(w.log)
	r, err := w.bot.GetUpdatesChan(telegram.UpdateConfig{})
	if err != nil {
		w.sql.Close()
		return &errval{s: "could not get Telegram receiver", e: err}
	}
	var (
		c = make(chan uint8, 64)
		s = make(chan os.Signal, 1)
		m = make(chan message, 256)
		t = make(chan *twitter.Tweet, 256)
		x context.Context
		g sync.WaitGroup
	)
	signal.Notify(s, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	x, w.cancel = context.WithCancel(context.Background())
	w.log.Info("Twitter Watcher Telegram Bot Started, spinning up threads..")
	go w.send(x, &g, m, t)
	go w.watch(x, &g, c, t)
	go w.receive(x, &g, m, r, c)
	go w.mentions(x, &g, w.notifier, t)
	for {
		select {
		case <-s:
			goto cleanup
		case <-w.tick.C:
			c <- 2
		case <-x.Done():
			goto cleanup
		}
	}
cleanup:
	signal.Stop(s)
	w.cancel()
	w.tick.Stop()
	w.bot.StopReceivingUpdates()
	g.Wait()
	close(c)
	close(s)
	close(m)
	close(t)
	if err := w.sql.Close(); err != nil {
		return err
	}
	return w.err
}

// New returns a new Watcher instance based on the passed config file path. This function will preform any
// setup steps needed to start the Watcher. Once complete, use the 'Run' function to actually start the Watcher.
// This function allows for specifying the option to clear the database before starting.
func New(s string, empty, update bool) (*Watcher, error) {
	var c config
	j, err := ioutil.ReadFile(s)
	if err != nil {
		return nil, &errval{s: `error reading config file "` + s + `"`, e: err}
	}
	if err := json.Unmarshal(j, &c); err != nil {
		return nil, &errval{s: `error parsing config file "` + s + `"`, e: err}
	}
	if err = c.check(); err != nil {
		return nil, err
	}
	l := logx.Multiple(logx.Console(logx.Level(c.Log.Level)))
	if len(c.Log.File) > 0 {
		f, err := logx.File(c.Log.File, logx.Append, logx.Level(c.Log.Level))
		if err != nil {
			return nil, &errval{s: `error setting up log file "` + c.Log.File + `"`, e: err}
		}
		l.Add(f)
	}
	l.SetPrintLevel(logx.Error)
	t := twitter.NewClient(
		oauth1.NewConfig(c.Twitter.ConsumerKey, c.Twitter.ConsumerSecret).Client(
			context.Background(), oauth1.NewToken(c.Twitter.AccessKey, c.Twitter.AccessSecret),
		),
	)
	if _, _, err := t.Accounts.VerifyCredentials(nil); err != nil {
		return nil, &errval{s: "login to Twitter failed", e: err}
	}
	var y *notifier
	if c.Mentions.Receiver != 0 && len(c.Mentions.Keywords) > 0 {
		y = &notifier{chat: c.Mentions.Receiver}
		if len(c.Mentions.Twitter.AccessKey) > 0 && len(c.Mentions.Twitter.AccessSecret) > 0 && len(c.Mentions.Twitter.ConsumerKey) > 0 && len(c.Mentions.Twitter.ConsumerSecret) > 0 {
			y.client = twitter.NewClient(
				oauth1.NewConfig(c.Twitter.ConsumerKey, c.Twitter.ConsumerSecret).Client(
					context.Background(), oauth1.NewToken(c.Twitter.AccessKey, c.Twitter.AccessSecret),
				),
			)
			if _, _, err := y.client.Accounts.VerifyCredentials(nil); err != nil {
				return nil, &errval{s: "login to Twitter mention acount failed", e: err}
			}
		}
		for _, k := range strings.Split(c.Mentions.Keywords, ",") {
			y.keywords = append(y.keywords, strings.TrimSpace(k))
		}
		if len(y.keywords) == 0 {
			y = nil
		}
	}
	b, err := telegram.NewBotAPI(c.Telegram)
	if err != nil {
		return nil, &errval{s: "login to Telegram failed", e: err}
	}
	d, err := sql.Open(
		"mysql",
		c.Database.Username+":"+c.Database.Password+"@"+c.Database.Server+"/"+c.Database.Name+"?multiStatements=true&interpolateParams=true",
	)
	if err != nil {
		return nil, &errval{s: `database connection "` + c.Database.Server + `" failed`, e: err}
	}
	if err = d.Ping(); err != nil {
		return nil, &errval{s: `database connection "` + c.Database.Server + `" failed`, e: err}
	}
	m := mapper.New(d)
	if d.SetConnMaxLifetime(c.Timeouts.Database); empty {
		if err = m.Batch(cleanStatements); err != nil {
			m.Close()
			return nil, &errval{s: "could not clean up database schema", e: err}
		}
	}
	if update {
		if err = m.Batch(upgradeStatements); err != nil {
			m.Close()
			return nil, &errval{s: "could not update database schema", e: err}
		}
	}
	if err = m.Batch(setupStatements); err != nil {
		m.Close()
		return nil, &errval{s: "could not set up database schema", e: err}
	}
	if err = m.Extend(queryStatements); err != nil {
		m.Close()
		return nil, &errval{s: "could not set up database schema", e: err}
	}
	return &Watcher{
		sql:      m,
		bot:      b,
		log:      l,
		tick:     time.NewTicker(c.Timeouts.Resolve),
		twitter:  t,
		backoff:  c.Timeouts.Backoff,
		allowed:  c.Allowed,
		blocked:  c.Blocked,
		confirm:  make(map[int64]struct{}),
		notifier: y,
	}, nil
}
