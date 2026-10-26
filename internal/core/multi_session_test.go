package core_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/crazyjums/myllmapp/internal/core"
	"github.com/crazyjums/myllmapp/internal/event"
	"github.com/crazyjums/myllmapp/internal/provider/fake"
)

// permWatcher 收集各会话的权限请求，让测试决定什么时候回答哪一个。
type permWatcher struct {
	mu   sync.Mutex
	reqs map[string][]string // 会话 ID → 请求 ID（按到达顺序）
	ch   chan string         // 每来一个请求通知一次（值是会话 ID）
}

func watchPerms(h *harness) *permWatcher {
	w := &permWatcher{reqs: map[string][]string{}, ch: make(chan string, 64)}
	h.engine.Bus().Subscribe(func(ev event.Event) {
		if p, ok := ev.Data.(event.PermissionRequest); ok {
			w.mu.Lock()
			w.reqs[ev.SessionID] = append(w.reqs[ev.SessionID], p.RequestID)
			w.mu.Unlock()
			w.ch <- ev.SessionID
		}
	})
	return w
}

// wait 等到会话 sid 来了第 n 个权限请求，返回它的请求 ID。
func (w *permWatcher) wait(t *testing.T, sid string, n int) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		w.mu.Lock()
		if len(w.reqs[sid]) >= n {
			id := w.reqs[sid][n-1]
			w.mu.Unlock()
			return id
		}
		w.mu.Unlock()
		select {
		case <-w.ch:
		case <-deadline:
			t.Fatalf("会话 %s 没有等到第 %d 个权限请求", sid, n)
		}
	}
}

func sendAsync(h *harness, sid, text string) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := h.engine.SendInputTo(context.Background(), sid, core.UserInput{Text: text})
		done <- err
	}()
	return done
}

func waitDone(t *testing.T, ch <-chan error) {
	t.Helper()
	select {
	case err := <-ch:
		if err != nil {
			t.Fatalf("发送失败: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("等待一轮结束超时")
	}
}

func storedUserTexts(h *harness, sid string) []string {
	s, err := h.store.LoadSession(context.Background(), sid)
	if err != nil {
		return nil
	}
	var out []string
	for _, m := range s.Messages {
		if m.IsUserInput() {
			out = append(out, m.Text())
		}
	}
	return out
}

// 两个会话同时运行：互不阻塞，权限请求各归各的会话，回答 B 不会放行 A，两边的历史互不串。
func TestTwoSessionsRunConcurrently(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, nil)
	w := watchPerms(h)

	a := h.engine.Status().SessionID
	doneA := sendAsync(h, "", "任务A")
	reqA := w.wait(t, a, 1) // A 停在 write_file 的确认上

	// 切到新会话：A 转入后台继续运行，新会话可以立即发消息。
	st, err := h.engine.NewSession()
	if err != nil {
		t.Fatalf("当前会话在运行时也应能新建会话: %v", err)
	}
	b := st.SessionID
	if b == a || st.Running || !slices.Equal(st.RunningSessions, []string{a}) {
		t.Fatalf("新会话不在运行，后台的 A 仍在运行: %+v", st)
	}
	doneB := sendAsync(h, "", "任务B")
	reqB := w.wait(t, b, 1)
	if st := h.engine.Status(); !st.Running || len(st.RunningSessions) != 2 || len(st.PendingPermissions) != 2 {
		t.Fatalf("两个会话都在运行、各有一个待确认: %+v", st)
	}

	// 同一个会话内仍然只能跑一轮。
	if _, err := h.engine.SendInputTo(context.Background(), a, core.UserInput{Text: "再来"}); !errors.Is(err, core.ErrBusy) {
		t.Fatalf("向正在运行的后台会话再发应返回 ErrBusy: %v", err)
	}

	// 先回答 B：B 往下走（shell 还会再问一次），A 不受影响，仍在等。
	if err := h.engine.RespondPermission(reqB, "allow"); err != nil {
		t.Fatal(err)
	}
	reqB2 := w.wait(t, b, 2)
	if len(w.reqs[a]) != 1 {
		t.Fatalf("回答 B 不应让 A 往下走: %v", w.reqs[a])
	}
	_ = h.engine.RespondPermission(reqB2, "allow")
	waitDone(t, doneB)
	if got := h.engine.Status(); got.Running || !slices.Equal(got.RunningSessions, []string{a}) {
		t.Fatalf("B 结束后只剩 A 在后台运行: %+v", got)
	}

	// 再回答 A。
	_ = h.engine.RespondPermission(reqA, "allow")
	reqA2 := w.wait(t, a, 2)
	_ = h.engine.RespondPermission(reqA2, "allow")
	waitDone(t, doneA)

	if got := h.engine.RunningSessions(); len(got) != 0 {
		t.Fatalf("全部结束后没有运行中的会话: %v", got)
	}
	if ua, ub := storedUserTexts(h, a), storedUserTexts(h, b); !slices.Equal(ua, []string{"任务A"}) || !slices.Equal(ub, []string{"任务B"}) {
		t.Fatalf("两个会话的历史互不串: A=%v B=%v", ua, ub)
	}
	// 事件都带着各自的会话 ID：A 的 turn_end 不会出现在 B 上。
	ends := map[string]int{}
	for _, ev := range h.rec.Events() {
		if _, ok := ev.Data.(event.TurnEnd); ok {
			ends[ev.SessionID]++
		}
	}
	if ends[a] != 1 || ends[b] != 1 {
		t.Fatalf("每个会话各有一个 turn_end: %v", ends)
	}
}

// 切回正在后台运行的会话，拿到的是内存里的实时对象；对它发送、取消都只影响它自己。
func TestSwitchBackToRunningSession(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, nil)
	w := watchPerms(h)
	ctx := context.Background()

	a := h.engine.Status().SessionID
	doneA := sendAsync(h, "", "任务A")
	reqA := w.wait(t, a, 1)
	b := mustNew(t, h)
	doneB := sendAsync(h, "", "任务B")
	w.wait(t, b, 1)

	loaded, err := h.engine.LoadSessionTail(ctx, a, 10)
	if err != nil {
		t.Fatalf("切回运行中的会话不应失败: %v", err)
	}
	if loaded.Status.SessionID != a || !loaded.Status.Running || len(loaded.Status.RunningSessions) != 2 {
		t.Fatalf("切回后当前会话是 A 且在运行: %+v", loaded.Status)
	}
	// 内存里的最新历史包含刚发的那条用户消息（存储里这时还没有，它要等本轮结束才保存）。
	found := false
	for _, ev := range loaded.Events {
		if u, ok := ev.Data.(event.UserMessage); ok && u.Text == "任务A" {
			found = true
		}
	}
	if !found {
		t.Fatal("重放里应有运行中会话的用户消息")
	}

	// 只取消 A：A 以 cancelled 结束，B 仍在等确认。
	h.engine.CancelSession(a)
	waitDone(t, doneA)
	if !h.engine.IsRunning(b) || h.engine.IsRunning(a) {
		t.Fatalf("取消 A 不应影响 B: %v", h.engine.RunningSessions())
	}
	_ = reqA
	h.engine.CancelSession(b)
	waitDone(t, doneB)
}

func mustNew(t *testing.T, h *harness) string {
	t.Helper()
	st, err := h.engine.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	return st.SessionID
}

// 后台会话的发送队列：目标会话不是当前会话（用户正看着别的会话）时也能接着发，且不会切换当前会话。
func TestSendToNonCurrentSession(t *testing.T) {
	h := newHarness(t, fake.Script{}, 0, nil) // 空剧本：只复述，不需要确认
	ctx := context.Background()

	a := h.engine.Status().SessionID
	if _, err := h.engine.SendInputTo(ctx, a, core.UserInput{Text: "第一句"}); err != nil {
		t.Fatal(err)
	}
	b := mustNew(t, h)

	// 当前是 B，向已结束、已保存的 A 再发一句。
	if _, err := h.engine.SendInputTo(ctx, a, core.UserInput{Text: "第二句"}); err != nil {
		t.Fatalf("向非当前会话发送应成功: %v", err)
	}
	if got := h.engine.Status().SessionID; got != b {
		t.Fatalf("发给后台会话不应切换当前会话: %s", got)
	}
	if !slices.Equal(storedUserTexts(h, a), []string{"第一句", "第二句"}) {
		t.Fatalf("A 的历史应包含两句: %v", storedUserTexts(h, a))
	}
	if len(storedUserTexts(h, b)) != 0 {
		t.Fatal("B 不应被写入")
	}
	if _, err := h.engine.SendInputTo(ctx, "sess_不存在", core.UserInput{Text: "x"}); err == nil {
		t.Fatal("不存在的会话应报错")
	}
}

// 运行中的后台会话：不能删除，改名改的是内存里的（否则结束时会被旧标题覆盖）。
func TestBackgroundSessionGuards(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, nil)
	w := watchPerms(h)
	ctx := context.Background()

	a := h.engine.Status().SessionID
	doneA := sendAsync(h, "", "任务A")
	reqA := w.wait(t, a, 1)
	_ = mustNew(t, h)

	if err := h.engine.DeleteSession(ctx, a); !errors.Is(err, core.ErrBusy) {
		t.Fatalf("运行中的后台会话不能删除: %v", err)
	}
	if _, err := h.engine.DeleteAllSessions(ctx); !errors.Is(err, core.ErrBusy) {
		t.Fatalf("有会话在运行时不能清空全部历史: %v", err)
	}
	if err := h.engine.RenameSession(ctx, a, "新名字"); err != nil {
		t.Fatal(err)
	}

	_ = h.engine.RespondPermission(reqA, "allow")
	_ = h.engine.RespondPermission(w.wait(t, a, 2), "allow")
	waitDone(t, doneA)
	saved, _ := h.store.LoadSession(ctx, a)
	if saved.Title != "新名字" {
		t.Fatalf("改名不应被本轮结束时的保存覆盖: %q", saved.Title)
	}
	if err := h.engine.DeleteSession(ctx, a); err != nil {
		t.Fatalf("结束后可以删除: %v", err)
	}
}

// “本会话总是允许”只对点它的那个会话生效。
func TestAllowSessionIsPerSession(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, nil)
	w := watchPerms(h)

	a := h.engine.Status().SessionID
	doneA := sendAsync(h, "", "A")
	_ = h.engine.RespondPermission(w.wait(t, a, 1), "allow_session") // write_file：本会话总是允许
	_ = h.engine.RespondPermission(w.wait(t, a, 2), "allow")         // shell
	waitDone(t, doneA)

	b := mustNew(t, h)
	doneB := sendAsync(h, "", "B")
	reqB := w.wait(t, b, 1) // B 仍然要问 write_file
	_ = h.engine.RespondPermission(reqB, "allow")
	_ = h.engine.RespondPermission(w.wait(t, b, 2), "allow")
	waitDone(t, doneB)

	// 回到 A 再来一轮：write_file 不再询问，只剩 shell 问一次。
	if _, err := h.engine.LoadSessionTail(context.Background(), a, 10); err != nil {
		t.Fatal(err)
	}
	doneA2 := sendAsync(h, "", "A2")
	_ = h.engine.RespondPermission(w.wait(t, a, 3), "allow")
	waitDone(t, doneA2)
	if n := len(w.reqs[a]); n != 3 {
		t.Fatalf("A 的第二轮应只问了 shell 一次（共 3 次）: %d", n)
	}
}

// 收到 turn_end 的那一刻就能发下一条（发送队列就是这样接着发的），不会撞上“上一轮还在收尾”的 ErrBusy，
// 并且两轮的历史按顺序保存。
func TestSendRightAfterTurnEnd(t *testing.T) {
	h := newHarness(t, fake.Script{}, 0, nil)
	const rounds = 20
	errs := make(chan error, rounds)
	var mu sync.Mutex
	sent := 1
	h.engine.Bus().Subscribe(func(ev event.Event) {
		if _, ok := ev.Data.(event.TurnEnd); !ok {
			return
		}
		mu.Lock()
		if sent >= rounds {
			mu.Unlock()
			return
		}
		sent++
		n := sent
		mu.Unlock()
		go func() {
			_, err := h.engine.SendInputTo(context.Background(), ev.SessionID, core.UserInput{Text: "第" + string(rune('A'+n-1)) + "句"})
			errs <- err
		}()
	})
	sid := h.engine.Status().SessionID
	if _, err := h.engine.SendInputTo(context.Background(), "", core.UserInput{Text: "第A句"}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < rounds-1; i++ {
		select {
		case err := <-errs:
			if err != nil {
				t.Fatalf("turn_end 之后立即发送失败: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("超时")
		}
	}
	// 等最后一轮收尾
	deadline := time.Now().Add(5 * time.Second)
	for len(storedUserTexts(h, sid)) < rounds && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := storedUserTexts(h, sid); len(got) != rounds {
		t.Fatalf("应保存 %d 轮，实际 %d: %v", rounds, len(got), got)
	}
}

// 还没保存的运行中会话也要出现在会话列表里，用户切走之后才能点回去。
func TestListSessionsIncludesRunningUnsaved(t *testing.T) {
	h := newHarness(t, fake.DemoScript(), 0, nil)
	w := watchPerms(h)
	ctx := context.Background()

	a := h.engine.Status().SessionID
	doneA := sendAsync(h, "", "任务A")
	reqA := w.wait(t, a, 1)
	_ = mustNew(t, h)

	list, err := h.engine.ListSessions(ctx)
	if err != nil || len(list) != 1 || list[0].ID != a || list[0].Title != "任务A" || list[0].MessageCount == 0 {
		t.Fatalf("存储里还没有 A，但列表里应有运行中的 A: %v %+v", err, list)
	}
	_ = h.engine.RespondPermission(reqA, "allow")
	_ = h.engine.RespondPermission(w.wait(t, a, 2), "allow")
	waitDone(t, doneA)
	list, _ = h.engine.ListSessions(ctx)
	if len(list) != 1 || list[0].ID != a {
		t.Fatalf("结束后以存储为准，不重复: %+v", list)
	}
}
