FROM ubuntu:14.04

# env vars
ENV HOME /root
ENV GOPATH /root/go
ENV PATH /root/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/games

# GOPATH
RUN mkdir -p $GOPATH

RUN apt-get update
RUN apt-get install -y build-essential mercurial git subversion wget curl cabal-install zlib1g-dev

# prepare cabal
RUN cabal update && cabal install cabal-install

# go 1.3 tarball
RUN wget -qO- http://golang.org/dl/go1.3.linux-amd64.tar.gz | tar -C /usr/local -xzf -

# project dependencies
RUN go get github.com/go-martini/martini
RUN go get github.com/martini-contrib/binding
RUN go get github.com/martini-contrib/render
RUN go get github.com/dchest/uniuri
RUN go get github.com/mattn/go-sqlite3
RUN go get github.com/jinzhu/gorm

# build the project
RUN mkdir -p $GOPATH/src/github.com/agrafix/hackageship
ADD . $GOPATH/src/github.com/agrafix/hackageship
RUN go build $GOPATH/src/github.com/agrafix/hackageship/hackageship.go

# copy the static stuff
RUN cp -r $GOPATH/src/github.com/agrafix/hackageship/public ./public
RUN cp -r $GOPATH/src/github.com/agrafix/hackageship/templates ./templates

# volume
VOLUME /data/state

# run
CMD ./hackageship -hackage-user="$HACKAGE_USER" -hackage-password="$HACKAGE_PASSWORD" -state-dir="/data/state"