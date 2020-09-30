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
	"encoding/json"
	"flag"
	"io/ioutil"
	"os"
	"strconv"
	"time"

	"github.com/PurpleSec/logx"

	// Import for the Golang MySQL driver
	_ "github.com/go-sql-driver/mysql"
)

const (
	bufferSize      = 256
	bufferSmallSize = 32

	timeoutWeb      = time.Second * 15
	timeoutResolve  = time.Hour * 6
	timeoutBackoff  = time.Millisecond * 250
	timeoutDatabase = time.Second * 60
)

const config = `{
	"db": {
		"host": "",
		"user": "",
		"password": "",
		"database": ""
	},
	"log": {
		"file": "watcher.log",
		"level": 2
	},
	"blocked": [],
	"allowed": [],
	"timeouts": {
		"web": 15000000000,
		"resolve": 21600000000000,
		"backoff": 250000000,
		"database": 60000000000,
		"telegram": 15000000000
	},
	"twitter": {
		"access_key": "",
		"consumer_key": "",
		"access_secret": "",
		"consumer_secret": ""
	},
	"telegram_key": ""
}
`
const usage = `Twitter Watcher Telegram Bot
iDigitalFlame 2020 (idigitalflame.com)

Usage:
  -h              Print this help menu.
  -f <file>       Configuration file path.
  -d              Dump the default configuration and exit.
  -c              Clear the database of ALL DATA before starting up.
`

var wake struct{}

type log struct {
	File  string `json:"file"`
	Level int    `json:"level"`
}
type auth struct {
	AccessKey      string `json:"access_key"`
	ConsumerKey    string `json:"consumer_key"`
	AccessSecret   string `json:"access_secret"`
	ConsumerSecret string `json:"consumer_secret"`
}

// Config is a struct that can be used to create and set parameters of the Watcher instance.
type Config struct {
	Log      log      `json:"log"`
	Twitter  auth     `json:"twitter"`
	Blocked  []string `json:"blocked"`
	Allowed  []string `json:"allowed"`
	Timeouts timeouts `json:"timeouts"`
	Telegram string   `json:"telegram_key"`
	Database database `json:"db"`
}
type timeouts struct {
	Web      time.Duration `json:"web"`
	Resolve  time.Duration `json:"resolver"`
	Backoff  time.Duration `json:"backoff"`
	Database time.Duration `json:"database"`
	Telegram time.Duration `json:"telegram"`
}
type database struct {
	Name     string `json:"database"`
	Server   string `json:"host"`
	Username string `json:"user"`
	Password string `json:"password"`
}

// Cmdline will create and process the Watcher instance based on the supplied command line arguments.
// This will quit using 'os.Exit' when an error occurs and will print a message to the standard error of the
// console. This function will block until operation completed.
func Cmdline() {
	var (
		args        = flag.NewFlagSet("Twitter Watcher Telegram Bot", flag.ExitOnError)
		file        string
		clear, dump bool
	)
	args.Usage = func() {
		os.Stderr.WriteString(usage)
		os.Exit(2)
	}
	args.StringVar(&file, "f", "", "Configuration file path.")
	args.BoolVar(&dump, "d", false, "Dump the default configuration and exit.")
	args.BoolVar(&clear, "c", false, "Clear the database of ALL DATA before starting up.")

	if err := args.Parse(os.Args[1:]); err != nil {
		os.Stderr.WriteString(usage)
		os.Exit(2)
	}

	if len(file) == 0 && !dump {
		os.Stderr.WriteString(usage)
		os.Exit(2)
	}

	if dump {
		os.Stdout.WriteString(config)
		os.Exit(0)
	}

	b, err := ioutil.ReadFile(file)
	if err != nil {
		os.Stderr.WriteString(`Error reading config file "` + file + `": ` + err.Error() + "!\n")
		os.Exit(1)
	}

	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		os.Stderr.WriteString(`Error parsing config file "` + file + `": ` + err.Error() + "!\n")
		os.Exit(1)
	}

	w, err := NewWatcher(c, clear)
	if err != nil {
		os.Stderr.WriteString("Error during setup: " + err.Error() + "!\n")
		os.Exit(1)
	}

	if err := w.Start(context.Background()); err != nil {
		os.Stderr.WriteString("Error during operation: " + err.Error() + "!\n")
		os.Exit(1)
	}
}
func (c *Config) check() error {
	if len(c.Twitter.AccessKey) == 0 {
		return &errorval{s: "missing Twitter access key"}
	}
	if len(c.Twitter.AccessSecret) == 0 {
		return &errorval{s: "missing Twitter access secret"}
	}
	if len(c.Twitter.ConsumerKey) == 0 {
		return &errorval{s: "missing Twitter consumer key"}
	}
	if len(c.Twitter.ConsumerSecret) == 0 {
		return &errorval{s: "missing Twitter consumer secret"}
	}
	if c.Log.Level > int(logx.Fatal) || c.Log.Level < int(logx.Trace) {
		return &errorval{s: `invalid log level "` + strconv.Itoa(c.Log.Level) + `"`}
	}
	if len(c.Database.Name) == 0 {
		return &errorval{s: "missing database name"}
	}
	if len(c.Database.Server) == 0 {
		return &errorval{s: "missing database server"}
	}
	if len(c.Database.Username) == 0 {
		return &errorval{s: "missing database username"}
	}
	if c.Timeouts.Web == 0 {
		c.Timeouts.Web = timeoutWeb
	}
	if c.Timeouts.Resolve == 0 {
		c.Timeouts.Resolve = timeoutResolve
	}
	if c.Timeouts.Backoff == 0 {
		c.Timeouts.Backoff = timeoutBackoff
	}
	if c.Timeouts.Database == 0 {
		c.Timeouts.Database = timeoutDatabase
	}
	if c.Timeouts.Telegram == 0 {
		c.Timeouts.Telegram = timeoutWeb
	}
	return nil
}
