package jobs

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/george518/PPGo_Job/internal/testdb"
	"github.com/george518/PPGo_Job/models"
	"golang.org/x/crypto/ssh"
)

// The fixture speaks real SSH over loopback, but never executes OS commands.
func mergeSSHFixture(t *testing.T) (int, string) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "client-key")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if c.User() != "fixture" || string(password) != "fixture-pass" {
				return nil, fmt.Errorf("invalid fixture credentials")
			}
			return nil, nil
		},
		PublicKeyCallback: func(c ssh.ConnMetadata, public ssh.PublicKey) (*ssh.Permissions, error) {
			if c.User() != "fixture" || !bytes.Equal(public.Marshal(), signer.PublicKey().Marshal()) {
				return nil, fmt.Errorf("invalid fixture key")
			}
			return nil, nil
		},
	}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	connections := make(map[net.Conn]bool)
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { conn.Close(); mu.Lock(); delete(connections, conn); mu.Unlock() }()
				conn.SetDeadline(time.Now().Add(5 * time.Second))
				server, channels, requests, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					if incoming.ChannelType() != "session" {
						incoming.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					channel, requests, err := incoming.Accept()
					if err != nil {
						return
					}
					for request := range requests {
						if request.Type != "exec" {
							request.Reply(false, nil)
							continue
						}
						var command struct{ Command string }
						if err := ssh.Unmarshal(request.Payload, &command); err != nil {
							request.Reply(false, nil)
							break
						}
						request.Reply(true, nil)
						io.WriteString(channel, "stdout:"+command.Command)
						status := uint32(0)
						if command.Command == "failure" {
							status = 7
							io.WriteString(channel.Stderr(), "fixture stderr")
						}
						channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
						break
					}
					channel.Close()
				}
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-acceptDone
		mu.Lock()
		for conn := range connections {
			conn.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return listener.Addr().(*net.TCPAddr).Port, keyPath
}

func TestMergedExecutionIntegration(t *testing.T) {
	testdb.Open(t, func() { models.Init(time.Now().Unix()) })
	port, keyPath := mergeSSHFixture(t)
	addServer := func(kind int, path string, port int) int {
		t.Helper()
		server := &models.TaskServer{ServerName: "SSH fixture", ServerIp: "127.0.0.1", Port: port, ServerAccount: "fixture", Password: "fixture-pass", PrivateKeySrc: path, Type: kind, Status: 0}
		id, err := models.TaskServerAdd(server)
		if err != nil {
			t.Fatal(err)
		}
		return int(id)
	}
	passwordServer := addServer(0, "", port)
	keyServer := addServer(1, keyPath, port)
	newTask := func(server int, command string) *models.Task {
		t.Helper()
		task := &models.Task{TaskName: "merged execution", ServerIds: strconv.Itoa(server), GroupId: 1, OwnerId: 7, CreateId: 1, CronSpec: "0 0 0 1 1 *", Command: command, Timeout: 1}
		if _, err := models.TaskAdd(task); err != nil {
			t.Fatal(err)
		}
		return task
	}
	checkRun := func(t *testing.T, task *models.Task, timeout bool, expected int, output, errPart string) {
		t.Helper()
		batch, err := NewJobFromTask(task)
		if err != nil || len(batch) != 1 {
			t.Fatalf("new job: %v %#v", err, batch)
		}
		if timeout {
			batch[0].RunFunc = func(time.Duration) *JobResult { return &JobResult{IsTimeout: true, ErrMsg: "fixture timeout"} }
		}
		batch[0].Run()
		logs, count := models.TaskLogGetList(1, 2, "task_id", task.Id)
		if count != 1 || len(logs) != 1 || logs[0].Status != expected || (output != "" && !strings.Contains(logs[0].Output, output)) || (errPart != "" && !strings.Contains(logs[0].Error, errPart)) {
			t.Fatalf("unexpected persisted result: count=%d logs=%+v", count, logs)
		}
		stored, err := models.TaskGetById(task.Id)
		if err != nil || stored.ExecuteTimes != 1 || stored.PrevTime == 0 || stored.OwnerId != 7 {
			t.Fatalf("task counters/owner: %+v %v", stored, err)
		}
		result := "success"
		if expected == models.TASK_ERROR {
			result = "failed"
		}
		if expected == models.TASK_TIMEOUT {
			result = "timeout"
		}
		search, err := models.TaskSearchList(models.TaskSearch{Page: 1, PageSize: 20, IsAdmin: true, TaskId: task.Id, LatestResult: result})
		if err != nil || search.Count != 1 {
			t.Fatalf("execution-to-search integration: %+v %v", search, err)
		}
	}
	for _, auth := range []struct {
		name   string
		server int
	}{{"password", passwordServer}, {"key", keyServer}} {
		t.Run(auth.name+"-success", func(t *testing.T) {
			checkRun(t, newTask(auth.server, "success"), false, models.TASK_SUCCESS, "stdout:success", "")
		})
		t.Run(auth.name+"-command-failure", func(t *testing.T) {
			checkRun(t, newTask(auth.server, "failure"), false, models.TASK_ERROR, "stdout:failure", "fixture stderr")
		})
	}
	t.Run("key-read-failure", func(t *testing.T) {
		id := addServer(1, filepath.Join(t.TempDir(), "missing-key"), port)
		checkRun(t, newTask(id, "success"), false, models.TASK_ERROR, "", "读取私钥失败")
	})
	t.Run("key-parse-failure", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "invalid-key")
		if err := os.WriteFile(path, []byte("invalid fixture key"), 0600); err != nil {
			t.Fatal(err)
		}
		checkRun(t, newTask(addServer(1, path, port), "success"), false, models.TASK_ERROR, "", "创建签名失败")
	})
	t.Run("connection-failure", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		closedPort := listener.Addr().(*net.TCPAddr).Port
		listener.Close()
		checkRun(t, newTask(addServer(0, "", closedPort), "success"), false, models.TASK_ERROR, "", "连接服务器失败")
	})
	t.Run("timeout-result", func(t *testing.T) {
		checkRun(t, newTask(passwordServer, "success"), true, models.TASK_TIMEOUT, "", "fixture timeout")
	})
	t.Run("polling-selection-and-connection-status", func(t *testing.T) {
		task := newTask(passwordServer, "success")
		task.ServerIds = fmt.Sprintf("%d,%d", passwordServer, keyServer)
		task.ServerType = 1
		if err := task.Update("ServerIds", "ServerType"); err != nil {
			t.Fatal(err)
		}
		batch, err := NewJobFromTask(task)
		if err != nil || len(batch) != 2 {
			t.Fatalf("jobs: %v", err)
		}
		counterKey := strconv.Itoa(task.Id)
		Counter.Store(counterKey, 0)
		t.Cleanup(func() { Counter.Delete(counterKey) })
		if PollServer(batch[1]) || !PollServer(batch[0]) {
			t.Fatal("incorrect first polling node")
		}
		SetCounter(counterKey)
		if PollServer(batch[0]) || !PollServer(batch[1]) {
			t.Fatal("incorrect second polling node")
		}
		server, err := models.TaskServerGetById(keyServer)
		if err != nil || server.Status != 0 {
			t.Fatalf("healthy node: %+v %v", server, err)
		}
		server.Password = "wrong-fixture-password"
		server.Type = 0
		if err := server.Update("Password", "Type"); err != nil {
			t.Fatal(err)
		}
		if PollServer(batch[1]) {
			t.Fatal("authentication failure should skip polling node")
		}
		server, err = models.TaskServerGetById(keyServer)
		if err != nil || server.Status != 1 {
			t.Fatalf("failed node: %+v %v", server, err)
		}
	})
}
