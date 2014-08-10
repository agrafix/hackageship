FROM ubuntu:14.04

# env vars
ENV HOME /root
ENV GOPATH /root/go
ENV PATH /root/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/usr/games

# GOPATH
RUN mkdir -p $GOPATH

RUN apt-get update
RUN apt-get install -y build-essential mercurial git subversion wget curl cabal-install

# go 1.3 tarball
RUN wget -qO- http://golang.org/dl/go1.3.linux-amd64.tar.gz | tar -C /usr/local -xzf -

ENV HOME /root
RUN go get github.com/go-martini/martini

RUN mkdir -p $GOPATH/src/github.com/agrafix/hackageship
ADD . $GOPATH/src/github.com/agrafix/hackageship
RUN go build $GOPATH/src/github.com/agrafix/hackageship/hackageship.go

CMD ./hackageship -secret="$GITHUB_SECRET"