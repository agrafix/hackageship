FROM mischief/docker-golang
ENV HOME /root
RUN go get github.com/go-martini/martini
RUN mkdir -p $GOPATH/src/github.com/agrafix/hackageship
ADD . $GOPATH/src/github.com/agrafix/hackageship
RUN go build $GOPATH/src/github.com/agrafix/hackageship/hackageship.go

CMD ./hackageship -secret="$GITHUB_SECRET"