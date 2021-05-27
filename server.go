package main

import (
	"fmt"
	"gopkg.in/yaml.v2"
	"log"
	"net/http"
	"os"

	"github.com/garyburd/redigo/redis"
	"github.com/gorilla/websocket"
)

type Config struct {
	Redis struct {
		Address string `yaml:"port"`
		Pass    string `yaml:"pass"`
	} `yaml:"redis"`
	Server struct {
		Port string `yaml:"port"`
	} `yaml:"server"`
}

func processError(err error) {
	fmt.Println(err)
	os.Exit(2)
}

func readFile(cfg *Config) {
	f, err := os.Open("config_example.yml")
	if err != nil {
		processError(err)
	}
	defer f.Close()

	decoder := yaml.NewDecoder(f)
	err = decoder.Decode(cfg)
	if err != nil {
		processError(err)
	}
}

var (
	redisConn = func(cfg *Config) (redis.Conn, error) {
		return redis.Dial("tcp", cfg.Redis.Address, redis.DialPassword(cfg.Redis.Pass))
	}
)

type User struct {
	ID            string
	websocketConn *websocket.Conn
	redisConn     *redis.PubSubConn
}

type Message struct {
	DeliveryID string `json:"id"`
	Content    string `json:"content"`
}

func newUser(conn *websocket.Conn, connection *redis.PubSubConn, id string) *User {
	u := &User{
		ID:            id,
		websocketConn: conn,
		redisConn:     connection,
	}

	if err := u.redisConn.Subscribe(u.ID); err != nil {
		panic(err)
	}
	return u
}

func removeUserByUser(user *User) {
	user.websocketConn.Close()
}

var cfg Config

func main() {
	readFile(&cfg)

	http.HandleFunc("/ws", wsHandler)

	log.Printf("server started at %s\n", cfg.Server.Port)
	log.Fatal(http.ListenAndServe(cfg.Server.Port, nil))
}

func getPubSub() (*redis.PubSubConn, error) {
	var redisConnection redis.Conn
	redisConnection, err := redisConn(&cfg)
	if err != nil {
		if redisConnection != nil {
			redisConnection.Close()
		}
		return nil, err
	}

	var pubSub *redis.PubSubConn
	pubSub = &redis.PubSubConn{Conn: redisConnection}
	return pubSub, nil
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	websocketConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrader error %s\n" + err.Error())
		return
	}

	var redisConnection *redis.PubSubConn
	redisConnection, err = getPubSub()
	if err != nil {
		log.Printf("Failed to generate websocket connection: " + err.Error())
		return
	}

	u := newUser(websocketConn, redisConnection, r.FormValue("id"))
	go deliverMessages(u)
	log.Printf("user %s joined\n", u.ID)

	websocketConn.SetCloseHandler(func(code int, text string) error {
		log.Printf("Connection ended by client")
		removeUserByUser(u)
		log.Printf("user removed from %s", u.ID)
		return nil
	})

	for {
		var m Message

		if err := u.websocketConn.ReadJSON(&m); err != nil {
			log.Printf("error on ws. message %s\n", err)
			if c, k := err.(*websocket.CloseError); k {
				if c.Code == 1000 || c.Code == 1001 || c.Code == 1005 {
					// Never entering since c.Code == 1005
					removeUserByUser(u)
					//log.Printf("2Removed user: %s\n", u.ID)
					break
				}
			}
		}

		if c, err := redisConn(&cfg); err != nil {
			log.Printf("error on redis websocketConn. %s\n", err)
		} else {
			c.Do("PUBLISH", m.DeliveryID, string(m.Content))
			log.Printf("publised %s into %s\n", m.Content, m.DeliveryID)
		}
	}
}

func deliverMessages(user *User) {
	for {
		switch v := user.redisConn.Receive().(type) {
		case redis.Message:
			deliverIfNeeded(v.Channel, string(v.Data), user)
		case redis.Subscription:
			log.Printf("subscription message: %s: %s %d\n", v.Channel, v.Kind, v.Count)
		case error:
			log.Println("error pub/sub on connection, delivery has stopped")
			return
		}
	}
}

func deliverIfNeeded(userID string, content string, user *User) {
	m := Message{
		Content: content,
	}

	if userID == user.ID {
		if err := user.websocketConn.WriteJSON(m); err != nil {
			log.Printf("error on message delivery through ws. e: %s\n", err)
			removeUserByUser(user)
		} else {
			//log.Printf("user %s found at our store, message sent\n", userID)
		}
	}
}
