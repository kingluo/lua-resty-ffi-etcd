// Copyright (c) 2023, Jinhua Luo (kingluo) luajit.io@gmail.com
// All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are met:
//
//  1. Redistributions of source code must retain the above copyright notice, this
//     list of conditions and the following disclaimer.
//
//  2. Redistributions in binary form must reproduce the above copyright notice,
//     this list of conditions and the following disclaimer in the documentation
//     and/or other materials provided with the distribution.
//
//  3. Neither the name of the copyright holder nor the names of its
//     contributors may be used to endorse or promote products derived from
//     this software without specific prior written permission.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
// AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
// IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
// DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
// FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
// DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
// SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
// CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
// OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
// OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
package main

/*
#cgo LDFLAGS: -shared
#include <string.h>
void* ngx_http_lua_ffi_task_poll(void *p);
char* ngx_http_lua_ffi_get_req(void *tsk, int *len);
void ngx_http_lua_ffi_respond(void *tsk, int rc, char* rsp, int rsp_len);
*/
import "C"
import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"sync"
	"time"

	"unsafe"

	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	CLIENT_NEW uint = iota
	CLIENT_CLOSE
	WATCH_CREATE
	WATCH_RECV
	WATCH_CLOSE
	KV_RANGE
	KV_PUT
)

type Command struct {
	task   unsafe.Pointer  `json:"-"`
	Cmd    uint            `json:"cmd"`
	Client uint64          `json:"client"`
	Req    json.RawMessage `json:"req"`
}

type CreateWatch struct {
	Timeout  *uint   `json:"timeout"`
	Key      string  `json:"key"`
	IsPrefix *bool   `json:"is_prefix"`
	Range    *string `json:"range"`
	Rev      *int64  `json:"rev"`
	Prev     *bool   `json:"prev"`
}

type watcher struct {
	clientv3.Watcher
	ch chan *Command
}

type client struct {
	*clientv3.Client
	watchersLock sync.RWMutex
	watcher_idx  uint64
	watchers     map[uint64]*watcher
}

//export libffi_init
func libffi_init(_ *C.char, tq unsafe.Pointer) C.int {
	go func() {
		var clientsLock sync.RWMutex
		var client_idx uint64
		clients := make(map[uint64]*client)

		for {
			task := C.ngx_http_lua_ffi_task_poll(tq)
			if task == nil {
				log.Println("exit lua-resty-ffi-etcd runtime")
				break
			}

			var rlen C.int
			r := C.ngx_http_lua_ffi_get_req(task, &rlen)
			data := C.GoBytes(unsafe.Pointer(r), rlen)
			var cmd Command
			err := json.Unmarshal(data, &cmd)
			if err != nil {
				log.Fatalln("error:", err)
			}
			cmd.task = task

			switch cmd.Cmd {
			case CLIENT_NEW:
				go func() {
					var cfg clientv3.Config
					err := json.Unmarshal(cmd.Req, &cfg)
					cli, err := clientv3.New(cfg)
					if err == nil {
						clientsLock.Lock()
						client_idx += 1
						clients[client_idx] = &client{Client: cli, watchers: make(map[uint64]*watcher)}
						clientsLock.Unlock()
						rsp := strconv.FormatUint(client_idx, 10)
						C.ngx_http_lua_ffi_respond(task, 0, (*C.char)(C.CString(rsp)), C.int(len(rsp)))
					} else {
						errStr := err.Error()
						C.ngx_http_lua_ffi_respond(cmd.task, 1, (*C.char)(C.CString(errStr)), C.int(len(errStr)))
					}
				}()
			case CLIENT_CLOSE:
				go func() {
					idx := cmd.Client
					clientsLock.Lock()
					if cli, ok := clients[cmd.Client]; ok {
						delete(clients, idx)
						clientsLock.Unlock()
						cli.watchersLock.Lock()
						for _, w := range cli.watchers {
							close(w.ch)
							w.Close()
						}
						cli.watchersLock.Unlock()
						cli.Close()
					} else {
						clientsLock.Unlock()
					}

					C.ngx_http_lua_ffi_respond(task, 0, nil, 0)
				}()
			case WATCH_CREATE:
				go func() {
					var data CreateWatch
					err := json.Unmarshal(cmd.Req, &data)
					if err != nil {
						log.Fatalln("error:", err)
					}
					var opts []clientv3.OpOption
					if data.IsPrefix != nil && *data.IsPrefix {
						ch := byte(data.Key[len(data.Key)-1])
						ch += 1
						key := data.Key[:len(data.Key)-1] + string(ch)
						opts = append(opts, clientv3.WithRange(key))
					}
					if data.Range != nil {
						opts = append(opts, clientv3.WithRange(*data.Range))
					}
					if data.Rev != nil {
						opts = append(opts, clientv3.WithRev(*data.Rev))
					}
					if data.Prev != nil && *data.Prev {
						opts = append(opts, clientv3.WithPrevKV())
					}

					var ctx context.Context
					var cancel context.CancelFunc
					if data.Timeout != nil {
						ctx, cancel = context.WithTimeout(context.Background(), time.Second*time.Duration(*data.Timeout))
					} else {
						ctx = context.Background()
					}

					clientsLock.RLock()
					cli := clients[cmd.Client]
					clientsLock.RUnlock()

					w := clientv3.NewWatcher(cli.Client)
					watchCh := w.Watch(ctx, data.Key, opts...)
					cmdCh := make(chan *Command)

					watcher := watcher{Watcher: w, ch: cmdCh}
					cli.watchersLock.Lock()
					cli.watcher_idx += 1
					idx := cli.watcher_idx
					cli.watchers[idx] = &watcher
					cli.watchersLock.Unlock()

					go func() {
						defer func() {
							if cancel != nil {
								cancel()
							}
						}()
						for {
							cmd, ok := <-cmdCh
							if !ok {
								break
							}
							watchRes, ok := <-watchCh
							if err := watchRes.Err(); err != nil {
								errStr := err.Error()
								C.ngx_http_lua_ffi_respond(cmd.task, 1, (*C.char)(C.CString(errStr)), C.int(len(errStr)))
							} else {
								rsp, err := json.Marshal(watchRes)
								if err != nil {
									log.Fatalln("error:", err)
								}
								C.ngx_http_lua_ffi_respond(cmd.task, 0, (*C.char)(C.CBytes(rsp)), C.int(len(rsp)))
							}
							if !ok {
								break
							}
						}
					}()
					rsp := strconv.FormatUint(idx, 10)
					C.ngx_http_lua_ffi_respond(task, 0, (*C.char)(C.CString(rsp)), C.int(len(rsp)))
				}()
			case WATCH_RECV:
				var idx uint64
				err := json.Unmarshal(cmd.Req, &idx)
				if err != nil {
					log.Fatalln("json.Unmarshal:", err)
				}

				clientsLock.RLock()
				cli, ok := clients[cmd.Client]
				clientsLock.RUnlock()
				if !ok {
					err := "invalid client"
					C.ngx_http_lua_ffi_respond(task, 0, (*C.char)(C.CString(err)), C.int(len(err)))
					continue
				}

				cli.watchersLock.RLock()
				w, ok := cli.watchers[idx]
				if !ok {
					cli.watchersLock.RUnlock()
					err := "invalid watcher"
					C.ngx_http_lua_ffi_respond(task, 1, (*C.char)(C.CString(err)), C.int(len(err)))
					continue
				}
				if len(w.ch) == 1 {
					C.ngx_http_lua_ffi_respond(task, 1, nil, 0)
				} else {
					w.ch <- &cmd
				}
				cli.watchersLock.RUnlock()
			case WATCH_CLOSE:
				go func() {
					var idx uint64
					err := json.Unmarshal(cmd.Req, &idx)
					if err != nil {
						log.Fatalln("json.Unmarshal:", err)
					}
					clientsLock.RLock()
					cli, ok := clients[cmd.Client]
					clientsLock.RUnlock()
					if ok {
						cli.watchersLock.Lock()
						if w, ok := cli.watchers[idx]; ok {
							delete(cli.watchers, idx)
							cli.watchersLock.Unlock()
							close(w.ch)
							w.Close()
						} else {
							cli.watchersLock.Unlock()
						}
					}
					C.ngx_http_lua_ffi_respond(task, 0, nil, 0)
				}()
			}
		}
	}()

	return 0
}

func main() {}
