package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"

	"gopkg.in/yaml.v2"

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
	cache     *Cache
	pubSub    *redis.PubSubConn
	redisConn = func(cfg *Config) (redis.Conn, error) {
		return redis.Dial("tcp", cfg.Redis.Address, redis.DialPassword(cfg.Redis.Pass))
	}
)

func init() {
	cache = &Cache{
		Users: make([]*User, 0, 1),
	}
}

type User struct {
	ID   string
	conn *websocket.Conn
}

type Cache struct {
	Users []*User
	mu    sync.Mutex
}

type Message struct {
	DeliveryID string `json:"id"`
	Content    string `json:"content"`
}

func (c *Cache) newUser(conn *websocket.Conn, id string) *User {
	u := &User{
		ID:   id,
		conn: conn,
	}

	if err := pubSub.Subscribe(u.ID); err != nil {
		panic(err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	c.Users = append(c.Users, u)
	return u
}

func (c *Cache) removeUserByIndex(index int) {
	if index == -1 {
		return
	}
	c.mu.Lock()
	u := c.Users[index]
	c.Users = append(c.Users[:index], c.Users[:index+1]...)
	c.mu.Unlock()
	u.conn.Close()
}

func user_pos(slice []*User, user *User) int {
	for p, u := range slice {
		if u == user {
			return p
		}
	}
	return -1
}
func (c *Cache) removeUserByUser(user *User) {
	c.mu.Lock()
	index := user_pos(c.Users, user)
	if index == -1 {
		c.mu.Unlock()
		return
	}
	u := c.Users[index]
	c.Users = append(c.Users[:index], c.Users[index+1:]...)
	c.mu.Unlock()
	u.conn.Close()
}

var cfg Config

func main() {
	readFile(&cfg)

	redisConn, err := redisConn(&cfg)
	if err != nil {
		panic(err)
	}
	defer redisConn.Close()

	pubSub = &redis.PubSubConn{Conn: redisConn}
	defer pubSub.Close()

	go deliverMessages()

	http.HandleFunc("/ws", wsHandler)

	log.Printf("server started at %s\n", cfg.Server.Port)
	log.Fatal(http.ListenAndServe(cfg.Server.Port, nil))
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrader error %s\n" + err.Error())
		return
	}

	u := cache.newUser(conn, r.FormValue("id"))
	log.Printf("user %s joined\n", u.ID)
	log.Printf("number of users %d", len(cache.Users))

	conn.SetCloseHandler(func(code int, text string) error {
		log.Printf("Connection ended by client")
		cache.removeUserByUser(u)
		log.Printf("user removed from %s", u.ID)
		log.Printf("number of users %d", len(cache.Users))
		return nil
	})

	for {
		var m Message

		if err := u.conn.ReadJSON(&m); err != nil {
			log.Printf("error on ws. message %s\n", err)
			if c, k := err.(*websocket.CloseError); k {
				if c.Code == 1000 || c.Code == 1001 || c.Code == 1005 {
					// Never entering since c.Code == 1005
					cache.removeUserByUser(u)
					//log.Printf("2Removed user: %s\n", u.ID)
					break
				}
			}
		}

		if c, err := redisConn(&cfg); err != nil {
			log.Printf("error on redis conn. %s\n", err)
		} else {
			c.Do("PUBLISH", m.DeliveryID, string(m.Content))
			log.Printf("publised %s into %s\n", m.Content, m.DeliveryID)
		}
	}
}

func deliverMessages() {
	for {
		switch v := pubSub.Receive().(type) {
		case redis.Message:
			cache.findAndDeliver(v.Channel, string(v.Data))
		case redis.Subscription:
			log.Printf("subscription message: %s: %s %d\n", v.Channel, v.Kind, v.Count)
		case error:
			log.Println("error pub/sub on connection, delivery has stopped")
			return
		}
	}
}

func (c *Cache) findAndDeliver(userID string, content string) {
	m := Message{
		Content: content,
	}
	//log.Printf("message : %s \n", content)
	/*for _, u := range c.Users {
		if u.ID == userID {
			if err := u.conn.WriteJSON(m); err != nil {
				log.Printf("error on message delivery through ws. e: %s\n", err)
			} else {
				log.Printf("user %s found at our store, message sent\n", userID)
			}
			//return
		}
	}*/

	for i := 0; i < len(c.Users); i++ {
		u := c.Users[i]
		if u.ID == userID {
			if err := u.conn.WriteJSON(m); err != nil {
				log.Printf("error on message delivery through ws. e: %s\n", err)
				c.removeUserByIndex(i)
				u.conn.Close()
				i--
			} else {
				//log.Printf("user %s found at our store, message sent\n", userID)
			}
		}
	}

	//log.Printf("user %s not found at our store\n", userID)
}
