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
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/PurpleSec/logx"
	"github.com/PurpleSec/mapper"

	twitter "github.com/g8rswimmer/go-twitter/v2"
	telegram "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Watcher is a struct that is used to manage the threads and processes used to
// control and operate the Telegram Watcher bot service.
type Watcher struct {
	log     logx.Log
	err     error
	sql     *mapper.Map
	bot     *telegram.BotAPI
	tick    *time.Ticker
	auth    string
	ck, cs  string
	cancel  context.CancelFunc
	confirm map[int64]struct{}
	allowed []string
	blocked []string
	backoff time.Duration
}
type message struct {
	msg   telegram.MessageConfig
	tries uint8
}

// Run will start the main Watcher process and all associated threads.
//
// This function will block until an interrupt signal is received. This function
// also returns any errors that occur during shutdown.
func (w *Watcher) Run() error {
	telegram.SetLogger(w.log)
	var (
		r = w.bot.GetUpdatesChan(telegram.UpdateConfig{})
		c = make(chan uint8, 64)
		s = make(chan os.Signal, 1)
		m = make(chan message, 256)
		t = make(chan *twitter.TweetObj, 256)
		x context.Context
		g sync.WaitGroup
	)
	signal.Notify(s, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	x, w.cancel = context.WithCancel(context.Background())
	w.log.Info("Twitter Watcher Telegram Bot Started, spinning up threads..")
	go w.send(x, &g, m, t)
	go w.watch(x, &g, c, t)
	go w.receive(x, &g, m, r, c)
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

// Add fulfils the Authenticator interface.
func (w *Watcher) Add(r *http.Request) {
	if len(w.auth) == 0 {
		return
	}
	r.Header.Add("Authorization", "Bearer "+w.auth)
}

// New returns a new Watcher instance based on the passed config file path.
//
// This function will preform any setup steps needed to start the Watcher. Once
// complete, use the 'Run' function to actually start the Watcher.
//
// This function allows for specifying the option to clear the database before
// starting.
func New(s string, empty, update bool) (*Watcher, error) {
	var c config
	j, err := os.ReadFile(s)
	if err != nil {
		return nil, errors.New(`reading config file "` + s + `": ` + err.Error())
	}
	if err = json.Unmarshal(j, &c); err != nil {
		return nil, errors.New(`parsing config file "` + s + `": ` + err.Error())
	}
	if err = c.check(); err != nil {
		return nil, err
	}
	l := logx.Multiple(logx.Console(logx.Level(c.Log.Level)))
	if len(c.Log.File) > 0 {
		var f logx.Log
		if f, err = logx.File(c.Log.File, logx.Append, logx.Level(c.Log.Level)); err != nil {
			return nil, errors.New(`setting up log file "` + c.Log.File + `": ` + err.Error())
		}
		l.Add(f)
	}
	l.SetPrintLevel(logx.Error)
	b, err := telegram.NewBotAPIWithClient(c.Telegram, telegram.APIEndpoint, &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: time.Second * 10, KeepAlive: time.Second * 30}).DialContext,
		MaxIdleConns:          64,
		IdleConnTimeout:       time.Second * 60,
		DisableKeepAlives:     false,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   time.Second * 10,
		ExpectContinueTimeout: time.Second * 10,
		ResponseHeaderTimeout: time.Second * 10,
	}})
	if err != nil {
		return nil, errors.New("telegram login: " + err.Error())
	}
	d, err := sql.Open(
		"mysql",
		c.Database.Username+":"+c.Database.Password+"@"+c.Database.Server+"/"+c.Database.Name+"?multiStatements=true&interpolateParams=true",
	)
	if err != nil {
		return nil, errors.New(`database connection "` + c.Database.Server + `": ` + err.Error())
	}
	if err = d.Ping(); err != nil {
		d.Close()
		return nil, errors.New(`database connection "` + c.Database.Server + `": ` + err.Error())
	}
	m := mapper.New(d)
	if d.SetConnMaxLifetime(c.Timeouts.Database); empty {
		if err = m.Batch(cleanStatements); err != nil {
			m.Close()
			return nil, errors.New("clean up database schema: " + err.Error())
		}
	}
	if update {
		if err = m.Batch(upgradeStatements); err != nil {
			m.Close()
			return nil, errors.New("upgrade database schema: " + err.Error())
		}
	}
	if err = m.Batch(setupStatements); err != nil {
		m.Close()
		return nil, errors.New("setup database schema: " + err.Error())
	}
	if err = m.Extend(queryStatements); err != nil {
		m.Close()
		return nil, errors.New("setup database schema: " + err.Error())
	}
	return &Watcher{
		ck:      c.Twitter.ConsumerKey,
		cs:      c.Twitter.ConsumerSecret,
		sql:     m,
		bot:     b,
		log:     l,
		tick:    time.NewTicker(c.Timeouts.Resolve),
		backoff: c.Timeouts.Backoff,
		allowed: c.Allowed,
		blocked: c.Blocked,
		confirm: make(map[int64]struct{}),
	}, nil
}
