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
	sql     *mapper.Map
	bot     *telegram.BotAPI
	log     logx.Log
	tick    *time.Ticker
	cancel  context.CancelFunc
	twitter *twitter.Client
	backoff time.Duration
	allowed []string
	blocked []string
}
type resolve struct {
	ID   int64
	TID  int64
	Name string
}
type message struct {
	msg   telegram.MessageConfig
	tries uint8
}
type errorval struct {
	e error
	s string
}

// Run will start the main Watcher process and all associated threads. This function will block until an
// interrupt signal is received. This function returns any errors that occur during shutdown.
func (w *Watcher) Run() error {
	return w.RunContext(context.Background())
}

// New returns a new Watcher instance based on the passed configuration. This function will preform any
// setup steps needed to start the Watcher. Once complete, use the 'Start' function to actually start the Watcher.
func New(c Config) (*Watcher, error) {
	l := logx.Multiple(logx.Console(logx.Level(c.Log.Level)))
	if len(c.Log.File) > 0 {
		f, err := logx.File(c.Log.File, logx.Append, logx.Level(c.Log.Level))
		if err != nil {
			return nil, &errorval{s: `error setting up log file "` + c.Log.File + `"`, e: err}
		}
		l.Add(f)
	}
	t := twitter.NewClient(
		oauth1.NewConfig(c.Twitter.ConsumerKey, c.Twitter.ConsumerSecret).Client(
			context.Background(), oauth1.NewToken(c.Twitter.AccessKey, c.Twitter.AccessSecret),
		),
	)
	if _, _, err := t.Accounts.VerifyCredentials(nil); err != nil {
		return nil, &errorval{s: "login to Twitter failed", e: err}
	}
	b, err := telegram.NewBotAPI(c.Telegram)
	if err != nil {
		return nil, &errorval{s: "login to Telegram failed", e: err}
	}
	d, err := sql.Open(
		"mysql",
		c.Database.Username+":"+c.Database.Password+"@"+c.Database.Server+"/"+c.Database.Name+"?multiStatements=true&interpolateParams=true",
	)
	if err != nil {
		return nil, &errorval{s: `database connection "` + c.Database.Server + `" failed`, e: err}
	}
	if err = d.Ping(); err != nil {
		return nil, &errorval{s: `database connection "` + c.Database.Server + `" failed`, e: err}
	}
	if d.SetConnMaxLifetime(c.Timeouts.Database); c.Clear {
		for i := range cleanStatements {
			if _, err := d.Exec(cleanStatements[i]); err != nil {
				d.Close()
				return nil, &errorval{s: "could not clean up database schema", e: err}
			}
		}
	}
	for i := range setupStatements {
		if _, err := d.Exec(setupStatements[i]); err != nil {
			d.Close()
			return nil, &errorval{s: "could not set up database schema", e: err}
		}
	}
	m := &mapper.Map{Database: d}
	if err = m.Extend(queryStatements); err != nil {
		m.Close()
		return nil, &errorval{s: "could not set up database schema", e: err}
	}
	return &Watcher{
		sql:     m,
		bot:     b,
		log:     l,
		tick:    time.NewTicker(c.Timeouts.Resolve),
		twitter: t,
		backoff: c.Timeouts.Backoff,
		allowed: c.Allowed,
		blocked: c.Blocked,
	}, nil
}

// RunContext will start the main Watcher process and all associated threads. This function will block until
// the context is cancled or an interrupt signal is received. This function returns any errors that occur during
// shutdown.
func (w *Watcher) RunContext(ctx context.Context) error {
	r, err := w.bot.GetUpdatesChan(telegram.UpdateConfig{})
	if err != nil {
		w.sql.Close()
		return &errorval{s: "could not get Telegram receiver", e: err}
	}
	var (
		c = make(chan uint8, 8)
		s = make(chan os.Signal, 1)
		m = make(chan message, 256)
		t = make(chan *twitter.Tweet, 256)
		x context.Context
		g sync.WaitGroup
	)
	signal.Notify(s, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	x, w.cancel = context.WithCancel(ctx)
	w.log.Info("Twitter Watcher Telegram Bot Started, spinning up threads...")
	go w.threadSend(x, &g, m, t)
	go w.threadTwitter(x, &g, c, t)
	go w.threadReceive(x, &g, m, r, c)
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
	w.cancel()
	w.tick.Stop()
	g.Wait()
	close(c)
	close(s)
	close(m)
	close(t)
	return w.sql.Close()
}
