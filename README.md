Hackage Ship
============

Ship your Haskell Cabal packages to Hackage without the hassle! This simple service hooks into github, builds cabal distribution .tar.gz and uploads them to Hackage. To see this live visit [hackageship.com](http://hackageship.com).

Run
---

Run using docker:

```bash
$ git clone https://github.com/agrafix/hackageship.git
$ cd hackageship
$ docker build -t [you]/hackageship .
$ PORT=80 DATA_DIR=[YOUR_DATA_DIRECTORY] HACKAGE_USER=[YOUR_HACKAGE_USER] HACKAGE_PASSWORD=[YOUR_HACKAGE_PASS] ./docker-run
```

Notes
---
You may wonder why this is written in Go and not Haskell - I am a passionate Haskell hacker, but I really wanted to try Go and I wanted to do something useful - so this is what came out :-)
