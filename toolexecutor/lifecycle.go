package toolexecutor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/toolprovider"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/tenantscope"
	"google.golang.org/grpc/metadata"
)

// Default reconnect backoff bounds.
const (
	DefaultBackoffBase = 500 * time.Millisecond
	DefaultBackoffMax  = 30 * time.Second
)

// SecretMetadataKey is the gRPC metadata key carrying the shared secret.
const SecretMetadataKey = toolprovider.ExecutorSecretMetadataKey

// Rejection records a tool the hub refused in an ExecutorHelloAck.
type Rejection struct {
	TenantID string
	Name     string
	Reason   string
}

func newInstanceID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// InstanceID returns the per-process instance id sent in every hello.
func (c *Client) InstanceID() string { return c.instanceID }

// Rejections returns every tool rejection reported by the hub so far.
func (c *Client) Rejections() []Rejection {
	c.rejMu.Lock()
	defer c.rejMu.Unlock()
	return append([]Rejection(nil), c.rejected...)
}

// Run keeps one ToolProviderService.Connect stream open per tenant scope,
// reconnecting with capped exponential backoff until ctx is cancelled.
func (c *Client) Run(ctx context.Context, svc llmv1.ToolProviderServiceClient, tenants []tenantscope.Scope) error {
	if svc == nil {
		return fmt.Errorf("toolexecutor: nil ToolProviderServiceClient")
	}
	if len(tenants) == 0 {
		return fmt.Errorf("toolexecutor: no tenants configured")
	}
	var wg sync.WaitGroup
	for _, sc := range tenants {
		wg.Add(1)
		go func(sc tenantscope.Scope) {
			defer wg.Done()
			c.RunTenant(ctx, svc, sc)
		}(sc)
	}
	wg.Wait()
	return ctx.Err()
}

// RunTenant runs the connect/reconnect loop for a single tenant until ctx ends.
func (c *Client) RunTenant(ctx context.Context, svc llmv1.ToolProviderServiceClient, sc tenantscope.Scope) {
	base, max := c.opts.BackoffBase, c.opts.BackoffMax
	if base <= 0 {
		base = DefaultBackoffBase
	}
	if max <= 0 {
		max = DefaultBackoffMax
	}
	if max < base {
		max = base
	}
	delay := base
	for {
		if ctx.Err() != nil {
			return
		}
		acked, err := c.connectOnce(ctx, svc, sc)
		if ctx.Err() != nil {
			return
		}
		if acked {
			delay = base // a healthy session resets the backoff
		}
		c.log(llmdlog.LevelWarn, "executor stream ended; reconnecting in %s: tenant=%s executor_id=%s instance_id=%s: %v",
			delay, sc.TenantID, c.opts.ExecutorID, c.instanceID, err)
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		if !acked {
			delay *= 2
			if delay > max {
				delay = max
			}
		}
	}
}

// connectOnce opens one stream, sends the hello, handles the ack and serves
// calls until the stream ends. acked reports whether the hub acknowledged.
func (c *Client) connectOnce(ctx context.Context, svc llmv1.ToolProviderServiceClient, sc tenantscope.Scope) (acked bool, err error) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	sctx = sc.OutgoingContext(sctx)
	if c.opts.SharedSecret != "" {
		sctx = metadata.AppendToOutgoingContext(sctx, SecretMetadataKey, c.opts.SharedSecret)
	}
	st, err := svc.Connect(sctx)
	if err != nil {
		return false, fmt.Errorf("connect: %w", err)
	}
	hello := c.Hello(c.instanceID)
	if err := st.Send(&llmv1.ToolProviderToServer{Frame: &llmv1.ToolProviderToServer_ExecutorHello{ExecutorHello: hello}}); err != nil {
		return false, fmt.Errorf("send hello: %w", err)
	}
	first, err := st.Recv()
	if err != nil {
		return false, fmt.Errorf("recv ack: %w", err)
	}
	ack := first.GetExecutorHelloAck()
	if ack == nil {
		c.log(llmdlog.LevelWarn, "first server frame is %T, not ExecutorHelloAck; closing stream: tenant=%s executor_id=%s",
			first.GetFrame(), sc.TenantID, c.opts.ExecutorID)
		return false, fmt.Errorf("unexpected first frame %T", first.GetFrame())
	}
	c.handleAck(sc, ack)
	err = c.Serve(sctx, st, sc)
	if errors.Is(err, io.EOF) {
		err = fmt.Errorf("stream closed by server: %w", err)
	}
	return true, err
}

func (c *Client) handleAck(sc tenantscope.Scope, ack *llmv1.ExecutorHelloAck) {
	c.log(llmdlog.LevelInfo, "executor hello acked: tenant=%s executor_id=%s instance_id=%s accepted=%v",
		sc.TenantID, c.opts.ExecutorID, c.instanceID, ack.GetAcceptedTools())
	for _, r := range ack.GetRejectedTools() {
		c.log(llmdlog.LevelWarn, "hub rejected tool: tenant=%s executor_id=%s name=%s reason=%s",
			sc.TenantID, c.opts.ExecutorID, r.GetName(), r.GetReason())
		c.rejMu.Lock()
		c.rejected = append(c.rejected, Rejection{TenantID: sc.TenantID, Name: r.GetName(), Reason: r.GetReason()})
		c.rejMu.Unlock()
	}
	if c.opts.OnAck != nil {
		c.opts.OnAck(sc, ack)
	}
}
