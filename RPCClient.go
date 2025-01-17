package fast

import (
    "context"
    "fmt"
    "sync"
    "sync/atomic"
    "time"
)

type (
    RPCClient struct {
        ClientHandler
        client   *Client
        handlers sync.Map
        state    int32
        onEvent  func(event RPCEvent, args ...interface{})
        rpcMap   RPCMap
    }
    RPCRequestHandler struct {
        respPayload []byte
        OnDone      func()
        Err         []byte
    }
    RPCEvent int
)

const (
    EventOpen       = RPCEvent(0)
    EventClose      = RPCEvent(1)
    EventError      = RPCEvent(2)
    EventAccept     = RPCEvent(3)
    EventServe      = RPCEvent(4)
    EventRawMessage = RPCEvent(5)
)

var (
    rpcId int32
)

func NewRPCClient() *RPCClient {
    c := &RPCClient{}
    c.rpcMap.init()
    return c
}
func (c *RPCClient) Go(ctx context.Context, name string, r interface{}, w interface{}, cb func(err error)) {
    Go(func() {
        err := c.Call(ctx, name, r, w)
        cb(err)
    })
}
func SendRawMessage()  {

}
func (c *RPCClient) Call(ctx context.Context, name string, r interface{}, w interface{}) (err error) {
    defer func() {
        if e := recover(); e != nil {
            err = fmt.Errorf("%v", e)
        }
    }()
    msg := newMessage(mTypeRequest)
    timeoutCh := time.After(time.Second * 120)

    defer msg.Release()
    msg.Id = atomic.AddInt32(&rpcId, 1)
    if r != nil {
        if payload, err := MSGPack(r); err != nil {
            return err
        } else {
            newName := name
            msg.Name = &newName
            msg.Payload = payload
        }
    }

    respChan := make(chan struct{})

    var (
        handler  = &RPCRequestHandler{}
        doneOnce sync.Once
    )

    handler.OnDone = func() {
        doneOnce.Do(func() {
            close(respChan)
        })
    }
    defer handler.OnDone()
    c.handlers.Store(msg.Id, handler)
    defer func() {
        c.handlers.Delete(msg.Id)
    }()
    sendData, _ := MSGPack(msg)

    c.client.SendPacket(sendData)

    select {
    case <-respChan:
        if handler.Err != nil {
            return fmt.Errorf("%s", handler.Err)
        }
        return MSGUnpack(handler.respPayload, w)
    case <-ctx.Done(): // 取消?
        return fmt.Errorf("cancel")
    case <-timeoutCh:
        return fmt.Errorf("request timeout")
    }
}

func (c *RPCClient) onMessage(client *Client, msg *message) {
    switch msg.Type {
    case mTypeResponse:
        c.handleResponse(client, msg)
    }
}
func (c *RPCClient) getHandler(id int32) (*RPCRequestHandler, bool) {
    p, ok := c.handlers.Load(id)
    if !ok {
        return nil, false
    }
    return p.(*RPCRequestHandler), true
}
func (c *RPCClient) handleResponse(client *Client, msg *message) {
    p, ok := c.handlers.Load(msg.Id)
    if !ok {
        return
    }
    h := p.(*RPCRequestHandler)
    h.respPayload = msg.Payload
    h.Err = msg.Err
    h.OnDone()
}

func (c *RPCClient) OnOpen(client *Client) {
    atomic.CompareAndSwapInt32(&c.state, 0, 1)
    if c.onEvent != nil {
        c.onEvent(EventOpen, client)
    }
}
func (c *RPCClient) OnClose(client *Client) {
    atomic.CompareAndSwapInt32(&c.state, 1, 0)
    c.handlers.Range(func(key, value interface{}) bool {
        if id, ok := key.(int32); ok {
            if h, ok2 := c.getHandler(id); ok2 {
                h.OnDone()
            }
        }
        return true
    })
    c.handlers = sync.Map{}

    if c.onEvent != nil {
        c.onEvent(EventClose, client)
    }
}
func (c *RPCClient) OnError(client *Client, err error) {
    if c.onEvent != nil {
        c.onEvent(EventError, client, err)
    }
}
func (c *RPCClient) HandlePacket(client *Client, packet *Packet) {
    payload := packet.Payload()
    msg := allocMessage()
    err := MSGUnpack(payload, msg)
    if err != nil {
        if c.onEvent != nil {
            c.onEvent(EventRawMessage, client, payload)
        }
        return
    }
    c.onMessage(client, msg)
}
func (c *RPCClient) Connect(network string, address string) {
    c.client = NewClient(network, c, Option{
        Address:       address,
        RetryDuration: time.Second,
    })
}
func (c *RPCClient) OnEvent(fn func(event RPCEvent, args ...interface{})) {
    c.onEvent = fn
}
func (c *RPCClient) IsConnected() bool {
    return atomic.LoadInt32(&c.state) == 1
}
func (c *RPCClient) AddPlugin(p interface{}) {
    pc := c.client.PacketConnection()
    if pc != nil {
        pc.AddPlugin(p)
    }
}
func (c *RPCClient) RegisterFunc(name string, fn interface{}) {
    c.rpcMap.RegisterFunc(name, fn)
}
