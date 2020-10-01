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
		"backoff": 5000000000,
		"resolve": 21600000000000,
		"database": 180000000000
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

// Config is a struct that can be used to create and set parameters of the Watcher instance. This struct can be
// used in the NewWaker function to return a Waker instance.
type Config struct {
	Log      log      `json:"log"`
	Clear    bool     `json:"-"`
	Twitter  auth     `json:"twitter"`
	Blocked  []string `json:"blocked"`
	Allowed  []string `json:"allowed"`
	Timeouts timeouts `json:"timeouts"`
	Telegram string   `json:"telegram_key"`
	Database database `json:"db"`
}
type timeouts struct {
	Resolve  time.Duration `json:"resolver"`
	Backoff  time.Duration `json:"backoff"`
	Database time.Duration `json:"database"`
}
type database struct {
	Name     string `json:"database"`
	Server   string `json:"host"`
	Username string `json:"user"`
	Password string `json:"password"`
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
	if c.Timeouts.Resolve == 0 {
		c.Timeouts.Resolve = time.Hour * 6
	}
	if c.Timeouts.Backoff == 0 {
		c.Timeouts.Backoff = time.Second * 5
	}
	if c.Timeouts.Database == 0 {
		c.Timeouts.Database = time.Minute * 3
	}
	return nil
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

// Cmdline will create and process the command line flags. This function will return the proper Config struct to
// use with 'NewWatcher'. This function will return an error if it occurs or "flag.ErrHelp" if the usage string
// is displayed. If config and the error are both nil, the default configuration is written to stdout and the process
// should exit with 0.
func Cmdline() (*Config, error) {
	var (
		args = flag.NewFlagSet("Twitter Watcher Telegram Bot", flag.ExitOnError)
		c    Config
		file string
		dump bool
	)
	args.Usage = func() {
		os.Stderr.WriteString(usage)
		os.Exit(2)
	}
	args.StringVar(&file, "f", "", "Configuration file path.")
	args.BoolVar(&dump, "d", false, "Dump the default configuration and exit.")
	args.BoolVar(&c.Clear, "c", false, "Clear the database of ALL DATA before starting up.")
	if err := args.Parse(os.Args[1:]); err != nil {
		os.Stderr.WriteString(usage)
		return nil, flag.ErrHelp
	}
	if len(file) == 0 && !dump {
		os.Stderr.WriteString(usage)
		return nil, flag.ErrHelp
	}
	if dump {
		os.Stdout.WriteString(config)
		return nil, nil
	}
	b, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, &errorval{s: `Error reading config file "` + file + `"`, e: err}
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, &errorval{s: `Error parsing config file "` + file + `"`, e: err}
	}
	return &c, c.check()
}
func stringLowMatch(s, m string) bool {
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
