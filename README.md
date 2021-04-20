# Requirements

* Golang-go

You might need to install the following packages: 
```bash
go get -u -v github.com/garyburd/redigo/redis
go get -u -v github.com/gorilla/websocket
go get gopkg.in/yaml.v2
```



# websocket-redis

Requires a running Redis instance.

Microservice for handling Redis pub/sub events and websocket connections.

Front-end client should make a websocket connection like so:

```javascript
var socket = new WebSocket("ws://127.0.0.1:8080/ws?id=123456");

socket.onmessage = function(event) {
    console.log(event);
};
```

Which creates and subscribes to a Redis channel (with an ID of 123456) and sends all events back to the client.

# Running

As a Docker container (Linux only):

```bash
$ CGO_ENABLED=0 GOOS=linux go build -a -tags netgo -ldflags '-w' .
$ docker build -t yourname/server .
```

Or just: ```go run server.go```
