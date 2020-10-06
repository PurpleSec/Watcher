# Twitter Watcher Telegram Bot

Golang Twitter Notification Bot for Telegram

This is a bot for Telegram written in Golang, backed by a SQL database that is used for subscribing to user Tweets.

## Command Line Options

```[text]
Twitter Watcher Telegram Bot
iDigitalFlame 2020 (idigitalflame.com)

Usage:
  -h              Print this help menu.
  -f <file>       Configuration file path.
  -d              Dump the default configuration and exit.
  -c              Clear the database of ALL DATA before starting up.
```

## Configuration Options

The default config can be dumped to Stdout using the '-d' command line flag.

```[json]
{
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
```
