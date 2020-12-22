// Copyright (C) 2020 - 2021 iDigitalFlame
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
	"strconv"
	"strings"
	"time"

	"github.com/PurpleSec/logx"

	// Import for the Golang MySQL driver
	_ "github.com/go-sql-driver/mysql"
)

// Defaults is a string representation of a JSON formatted default configuration for a Watcher instance.
const Defaults = `{
	"db": {
		"host": "tcp(localhost:3306)",
		"user": "watcher_user",
		"password": "password",
		"database": "watcher_db"
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

const (
	errmsg = `I'm sorry, There seems to have been an error trying to process your request

Please try again later.`
	invalid = `I'm sorry I don't understand that command.

Please use a command from the following list:
/list
/clear
/add <@username1,@usernameN,..>
/remove <@username1,@usernameN,..|clear|all>`
)

type errval struct {
	e error
	s string
}
type config struct {
	Log struct {
		File  string `json:"file"`
		Level int    `json:"level"`
	} `json:"log"`
	Twitter struct {
		AccessKey      string `json:"access_key"`
		ConsumerKey    string `json:"consumer_key"`
		AccessSecret   string `json:"access_secret"`
		ConsumerSecret string `json:"consumer_secret"`
	} `json:"twitter"`
	Blocked  []string `json:"blocked"`
	Allowed  []string `json:"allowed"`
	Mentions struct {
		Keywords string `json:"keywords"`
		Receiver int64  `json:"chat"`
	} `json:"mentions"`
	Timeouts struct {
		Resolve  time.Duration `json:"resolver"`
		Backoff  time.Duration `json:"backoff"`
		Database time.Duration `json:"database"`
	} `json:"timeouts"`
	Telegram string `json:"telegram_key"`
	Database struct {
		Name     string `json:"database"`
		Server   string `json:"host"`
		Username string `json:"user"`
		Password string `json:"password"`
	} `json:"db"`
}

func isValid(s string) bool {
	if len(s) == 0 || s[0] != '@' || len(s) > 16 {
		return false
	}
	for i := range s {
		if i == 0 {
			continue
		}
		switch {
		case s[i] == '_':
			continue
		case s[i] < 48 || s[i] > 122:
			return false
		case s[i] > 57 && s[i] < 65:
			return false
		case s[i] > 90 && s[i] < 96:
			return false
		}
	}
	return true
}
func (e errval) Error() string {
	if e.e == nil {
		return e.s
	}
	return e.s + ": " + e.e.Error()
}
func (c *config) check() error {
	if len(c.Twitter.AccessKey) == 0 {
		return &errval{s: "missing Twitter access key"}
	}
	if len(c.Twitter.AccessSecret) == 0 {
		return &errval{s: "missing Twitter access secret"}
	}
	if len(c.Twitter.ConsumerKey) == 0 {
		return &errval{s: "missing Twitter consumer key"}
	}
	if len(c.Twitter.ConsumerSecret) == 0 {
		return &errval{s: "missing Twitter consumer secret"}
	}
	if c.Log.Level > int(logx.Fatal) || c.Log.Level < int(logx.Trace) {
		return &errval{s: `invalid log level "` + strconv.Itoa(c.Log.Level) + `"`}
	}
	if len(c.Database.Name) == 0 {
		return &errval{s: "missing database name"}
	}
	if len(c.Database.Server) == 0 {
		return &errval{s: "missing database server"}
	}
	if len(c.Database.Username) == 0 {
		return &errval{s: "missing database username"}
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
func stringSplitContains(s, m string) bool {
	for i, e := 0, strings.IndexByte(m, ','); i < len(m); i, e = e+1, strings.IndexByte(m[e+1:], ',') {
		if e == -1 {
			if strings.Contains(s, m[i:]) {
				return true
			}
			break
		}
		if e += i; strings.Contains(s, m[i:e]) {
			return true
		}
	}
	return false
}
func canUseACL(n string, a, d []string) bool {
	if len(d) == 0 && len(a) == 0 {
		return true
	}
	if len(d) > 0 {
		for i := range d {
			if stringLowMatch(n, d[i]) {
				return false
			}
		}
	}
	if len(a) == 0 {
		return true
	}
	for i := range a {
		if stringLowMatch(n, a[i]) {
			return true
		}
	}
	return false
}
func split(s string) ([]string, string, string) {
	var (
		z    = strings.IndexByte(s, ' ')
		v, k = s, ""
		r    []string
		t    string
	)
	if z > 0 {
		k, v = s[z+1:], s[:z]
	}
	for i, e := 0, strings.IndexByte(v, ','); i < len(v); i, e = e+1, strings.IndexByte(v[e+1:], ',') {
		if e == -1 {
			if t = strings.TrimSpace(v[i:]); !isValid(t) {
				return nil, k, `The username "` + t + `" is not a valid Twitter username!` + "\n\nTwitter names must start with \"@\" and contain no special characters or spaces."
			}
			r = append(r, t[1:])
			break
		}
		if e, t = e+i, strings.TrimSpace(v[i:e]); !isValid(t) {
			return nil, k, `The username "` + t + `" is not a valid Twitter username!` + "\n\nTwitter names must start with \"@\" and contain no special characters or spaces."
		}
		r = append(r, t[1:])
	}
	return r, k, ""
}
