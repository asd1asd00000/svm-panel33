package sshvpn

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/asd1asd00000/svm-panel/database"
	"golang.org/x/crypto/ssh"
)

type SyncUser struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	DataLimit  int64  `json:"data_limit"`
	DataUsed   int64  `json:"data_used"`
	ExpiryUnix int64  `json:"expiry_unix"`
}

type NodeSession struct {
	Limit int64
	Used  int64
	Conns []ssh.Conn
}

var (
	activeUsers       = make(map[string]int)
	activeUsersMutex  sync.Mutex
	localUsers        = make(map[string]SyncUser)
	localUsersMutex   sync.RWMutex
	nodeSessions      = make(map[string]*NodeSession)
	nodeSessionsMutex sync.Mutex
	pendingUsage      = make(map[string]int64)
	pendingUsageMutex sync.Mutex
	nodeOnlineCache   = make(map[string]int64)
	nodeOnlineMutex   sync.Mutex
)

func UpdateNodeOnlineStatus(users []string) {
	nodeOnlineMutex.Lock()
	now := time.Now().Unix()
	for _, u := range users {
		nodeOnlineCache[u] = now
	}
	nodeOnlineMutex.Unlock()
}

// اصلاح حیاتی: حالا این تابع هم کاربران سرور اصلی و هم نودها را به پنل لینوکس پاس می‌دهد
func GetOnlineUsersList() []string {
	var list []string
	now := time.Now().Unix()
	onlineMap := make(map[string]bool)

	activeUsersMutex.Lock()
	for u, count := range activeUsers {
		if count > 0 {
			onlineMap[u] = true
		}
	}
	activeUsersMutex.Unlock()

	nodeOnlineMutex.Lock()
	for u, lastSeen := range nodeOnlineCache {
		if now-lastSeen < 30 {
			onlineMap[u] = true
		}
	}
	nodeOnlineMutex.Unlock()

	for u := range onlineMap {
		list = append(list, u)
	}
	return list
}

func IsUserOnline(username string) bool {
	activeUsersMutex.Lock()
	localOnline := activeUsers[username] > 0
	activeUsersMutex.Unlock()

	if localOnline {
		return true
	}

	nodeOnlineMutex.Lock()
	lastSeen := nodeOnlineCache[username]
	nodeOnlineMutex.Unlock()

	return time.Now().Unix()-lastSeen < 30
}

type chanWriter struct {
	w        io.Writer
	username string
}

func (cw chanWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	if n > 0 {
		nodeSessionsMutex.Lock()
		if sess, exists := nodeSessions[cw.username]; exists {
			sess.Used += int64(n)
		}
		nodeSessionsMutex.Unlock()

		pendingUsageMutex.Lock()
		pendingUsage[cw.username] += int64(n)
		pendingUsageMutex.Unlock()
	}
	return n, err
}

func StartSSHServer(listenAddr string, isNode bool, mainURL, token string) {
	config := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if !isNode {
				var dbPassword string
				var expiryDate time.Time
				var dataLimit, dataUsed int64
				query := "SELECT password, expiry_date, data_limit, data_used FROM users WHERE username = ?"
				err := database.DB.QueryRow(query, c.User()).Scan(&dbPassword, &expiryDate, &dataLimit, &dataUsed)
				if err != nil || string(password) != dbPassword || time.Now().After(expiryDate) || dataUsed >= dataLimit {
					return nil, fmt.Errorf("auth failed")
				}
				return nil, nil
			}

			localUsersMutex.RLock()
			user, exists := localUsers[c.User()]
			localUsersMutex.RUnlock()

			if !exists || string(password) != user.Password {
				return nil, fmt.Errorf("invalid credentials")
			}
			if time.Now().Unix() > user.ExpiryUnix {
				return nil, fmt.Errorf("account expired")
			}
			if user.DataUsed >= user.DataLimit {
				return nil, fmt.Errorf("data limit reached")
			}
			return nil, nil
		},
	}

	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	signer, _ := ssh.ParsePrivateKey(privateKeyPEM)
	config.AddHostKey(signer)

	if isNode {
		go func() {
			client := &http.Client{Timeout: 5 * time.Second}
			for {
				reqURL := fmt.Sprintf("%s/api/online?token=%s", mainURL, token)
				resp, err := client.Post(reqURL, "application/json", bytes.NewBuffer([]byte("[]")))
				if err == nil {
					resp.Body.Close()
				}
				time.Sleep(20 * time.Second)
			}
		}()

		go func() {
			httpClient := &http.Client{Timeout: 5 * time.Second}
			for {
				resp, err := httpClient.Get(fmt.Sprintf("%s/api/users?token=%s", mainURL, token))
				if err == nil {
					var list []SyncUser
					if json.NewDecoder(resp.Body).Decode(&list) == nil {
						localUsersMutex.Lock()
						for _, u := range list {
							localUsers[u.Username] = u
						}
						localUsersMutex.Unlock()
					}
					resp.Body.Close()
				}
				time.Sleep(15 * time.Second)
			}
		}()

		go func() {
			usageClient := &http.Client{Timeout: 5 * time.Second}
			for {
				time.Sleep(4 * time.Second)
				
				var onlineList []string
				activeUsersMutex.Lock()
				for u, count := range activeUsers {
					if count > 0 {
						onlineList = append(onlineList, u)
					}
				}
				activeUsersMutex.Unlock()

				jsonBytesOnline, _ := json.Marshal(onlineList)
				reqURLOnline := fmt.Sprintf("%s/api/online?token=%s", mainURL, token)
				respOnline, errOnline := usageClient.Post(reqURLOnline, "application/json", bytes.NewBuffer(jsonBytesOnline))
				if errOnline == nil {
					respOnline.Body.Close()
				}

				pendingUsageMutex.Lock()
				snapshot := make(map[string]int64)
				for u, b := range pendingUsage {
					if b > 0 {
						snapshot[u] = b
						pendingUsage[u] = 0
					}
				}
				pendingUsageMutex.Unlock()

				for user, bytesAdded := range snapshot {
					payload := map[string]interface{}{"username": user, "bytes_added": bytesAdded}
					jsonBytesUsage, _ := json.Marshal(payload)
					reqURLUsage := fmt.Sprintf("%s/api/usage?token=%s", mainURL, token)
					respUsage, errUsage := usageClient.Post(reqURLUsage, "application/json", bytes.NewBuffer(jsonBytesUsage))
					if errUsage == nil {
						respUsage.Body.Close()
					} else {
						pendingUsageMutex.Lock()
						pendingUsage[user] += bytesAdded
						pendingUsageMutex.Unlock()
					}
				}

				nodeSessionsMutex.Lock()
				for username, sess := range nodeSessions {
					localUsersMutex.RLock()
					lu, exists := localUsers[username]
					localUsersMutex.RUnlock()
					if exists && (lu.DataUsed >= lu.DataLimit || sess.Used >= lu.DataLimit) {
						for _, conn := range sess.Conns {
							conn.Close()
						}
					}
				}
				nodeSessionsMutex.Unlock()
			}
		}()
	} else {
		go func() {
			for {
				time.Sleep(3 * time.Second)
				now := time.Now().Unix()
				_ = database.UpdateNodeLastSeen("Main-Server", now)

				pendingUsageMutex.Lock()
				snapshot := make(map[string]int64)
				for u, b := range pendingUsage {
					if b > 0 {
						snapshot[u] = b
						pendingUsage[u] = 0
					}
				}
				pendingUsageMutex.Unlock()

				for user, bytesAdded := range snapshot {
					_ = database.IncrementUserDataUsed(user, bytesAdded, "Main-Server")
					_ = database.IncrementNodeTraffic("Main-Server", bytesAdded)
				}

				nodeSessionsMutex.Lock()
				for username, sess := range nodeSessions {
					_ = database.UpdateLastSeen(username, now)
					var dataLimit, dataUsed int64
					err := database.DB.QueryRow("SELECT data_limit, data_used FROM users WHERE username = ?", username).Scan(&dataLimit, &dataUsed)
					if err == nil && (dataUsed >= dataLimit || sess.Used >= dataLimit) {
						for _, conn := range sess.Conns {
							conn.Close()
						}
					}
				}
				nodeSessionsMutex.Unlock()
			}
		}()
	}

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatal(err)
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleConnection(conn, config, isNode)
	}
}

func handleConnection(nConn net.Conn, config *ssh.ServerConfig, isNode bool) {
	sConn, chans, reqs, err := ssh.NewServerConn(nConn, config)
	if err != nil {
		return
	}
	username := sConn.User()
	
	activeUsersMutex.Lock()
	activeUsers[username]++
	activeUsersMutex.Unlock()

	nodeSessionsMutex.Lock()
	sess, exists := nodeSessions[username]
	if !exists {
		var dataLimit, dataUsed int64
		if !isNode {
			_ = database.DB.QueryRow("SELECT data_limit, data_used FROM users WHERE username = ?", username).Scan(&dataLimit, &dataUsed)
		} else {
			localUsersMutex.RLock()
			lu := localUsers[username]
			localUsersMutex.RUnlock()
			dataLimit = lu.DataLimit
			dataUsed = lu.DataUsed
		}
		sess = &NodeSession{Limit: dataLimit, Used: dataUsed, Conns: []ssh.Conn{}}
		nodeSessions[username] = sess
	}
	sess.Conns = append(sess.Conns, sConn)
	nodeSessionsMutex.Unlock()

	defer func() {
		sConn.Close()
		nodeSessionsMutex.Lock()
		if s := nodeSessions[username]; s != nil {
			for i, c := range s.Conns {
				if c == sConn {
					s.Conns = append(s.Conns[:i], s.Conns[i+1:]...)
					break
				}
			}
			if len(s.Conns) == 0 {
				delete(nodeSessions, username)
			}
		}
		nodeSessionsMutex.Unlock()

		activeUsersMutex.Lock()
		activeUsers[username]--
		activeUsersMutex.Unlock()
	}()

	go ssh.DiscardRequests(reqs)
	for newChannel := range chans {
		if newChannel.ChannelType() != "direct-tcpip" {
			newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}
		go handleProxyChannel(username, newChannel)
	}
}

func handleProxyChannel(username string, newChannel ssh.NewChannel) {
	type channelOpenDirectMsg struct {
		Raddr string
		Rport uint32
	}
	var msg channelOpenDirectMsg
	_ = ssh.Unmarshal(newChannel.ExtraData(), &msg)

	targetConn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", msg.Raddr, msg.Rport))
	if err != nil {
		newChannel.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	defer targetConn.Close()

	connection, requests, _ := newChannel.Accept()
	defer connection.Close()
	go ssh.DiscardRequests(requests)

	go func() {
		cw := chanWriter{w: connection, username: username}
		_, _ = io.Copy(cw, targetConn)
		connection.CloseWrite()
	}()

	cw2 := chanWriter{w: targetConn, username: username}
	_, _ = io.Copy(cw2, connection)
}
