package permission

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestModePolicy(t *testing.T) {
	cases := []struct {
		mode Mode
		risk Risk
		want Verdict
	}{
		{ModeDefault, RiskRead, Allow},
		{ModeDefault, RiskWrite, Ask},
		{ModeDefault, RiskExec, Ask},
		{ModeAuto, RiskExec, Allow},
		{ModeReadOnly, RiskWrite, Deny},
		{ModeReadOnly, RiskRead, Allow},
		{ModeAcceptEdits, RiskRead, Allow},
		{ModeAcceptEdits, RiskWrite, Allow},
		{ModeAcceptEdits, RiskExec, Ask},
		{Mode("typo"), RiskWrite, Ask}, // 未知模式回落到最保守的 default
	}
	for _, c := range cases {
		if got := NewModePolicy(c.mode).Evaluate("t", c.risk, nil); got != c.want {
			t.Errorf("mode=%s risk=%s: got %s want %s", c.mode, c.risk, got, c.want)
		}
	}
}

func TestSetMode(t *testing.T) {
	p := NewModePolicy(ModeDefault)
	p.Remember("shell", DecisionAllowSession)
	if err := p.SetMode(ModeReadOnly); err != nil || p.Mode() != ModeReadOnly {
		t.Fatalf("SetMode: %v %s", err, p.Mode())
	}
	if p.Evaluate("shell", RiskExec, nil) != Deny {
		t.Fatal("readonly 应拒绝执行，即使之前允许过")
	}
	if err := p.SetMode(ModeDefault); err != nil || p.Evaluate("shell", RiskExec, nil) != Allow {
		t.Fatal("切回 default 后应保留本会话的允许记忆")
	}
	if err := p.SetMode("typo"); err == nil || p.Mode() != ModeDefault {
		t.Fatal("未知模式应报错且不改变当前模式")
	}
}

func TestModePolicyRememberSession(t *testing.T) {
	p := NewModePolicy(ModeDefault)
	p.Remember("write_file", DecisionAllow) // 仅本次允许，不应被记住
	if p.Evaluate("write_file", RiskWrite, nil) != Ask {
		t.Fatal("allow 不应被记住")
	}
	p.Remember("write_file", DecisionAllowSession)
	if p.Evaluate("write_file", RiskWrite, nil) != Allow {
		t.Fatal("allow_session 后应直接放行")
	}
	if p.Evaluate("shell", RiskExec, nil) != Ask {
		t.Fatal("记忆只对同名工具生效")
	}
	p.Reset()
	if p.Evaluate("write_file", RiskWrite, nil) != Ask {
		t.Fatal("Reset 后应重新询问")
	}
}

func TestBrokerResolveInsideAnnounce(t *testing.T) {
	// 模拟 CLI：在通知回调里同步回答。先登记后通知的顺序保证这不会丢失回答或死锁。
	b := NewBroker()
	d, err := b.Request(context.Background(), Request{ID: "p1"}, func(r Request) {
		if err := b.Resolve(r.ID, DecisionAllow); err != nil {
			t.Errorf("resolve: %v", err)
		}
	})
	if err != nil || d != DecisionAllow {
		t.Fatalf("got %v %v", d, err)
	}
	if len(b.PendingIDs()) != 0 {
		t.Fatal("完成后应清理")
	}
	if err := b.Resolve("p1", DecisionAllow); !errors.Is(err, ErrUnknownRequest) {
		t.Fatalf("重复回复应报错: %v", err)
	}
}

func TestBrokerResolveFromAnotherGoroutine(t *testing.T) {
	b := NewBroker()
	go func() {
		for len(b.PendingIDs()) == 0 {
			time.Sleep(time.Millisecond)
		}
		_ = b.Resolve("p1", DecisionDeny)
	}()
	d, err := b.Request(context.Background(), Request{ID: "p1"}, nil)
	if err != nil || d != DecisionDeny {
		t.Fatalf("got %v %v", d, err)
	}
}

func TestBrokerCancel(t *testing.T) {
	b := NewBroker()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d, err := b.Request(ctx, Request{ID: "p1"}, nil)
	if !errors.Is(err, context.Canceled) || d.Allowed() {
		t.Fatalf("取消应返回拒绝和 ctx 错误: %v %v", d, err)
	}
}

func TestBrokerInvalidDecision(t *testing.T) {
	b := NewBroker()
	if err := b.Resolve("x", Decision("yes")); err == nil {
		t.Fatal("非法决定应报错")
	}
}

func TestModePolicySetMode(t *testing.T) {
	p := NewModePolicy(ModeDefault)
	var _ ModeSwitcher = p
	if err := p.SetMode(ModeReadOnly); err != nil || p.Mode() != ModeReadOnly {
		t.Fatalf("切换失败: %v %s", err, p.Mode())
	}
	if p.Evaluate("write_file", RiskWrite, nil) != Deny {
		t.Fatal("切换后应立即按新模式裁决")
	}
	if err := p.SetMode(Mode("typo")); err == nil || p.Mode() != ModeReadOnly {
		t.Fatal("未知模式应报错且不改变当前模式")
	}
}
