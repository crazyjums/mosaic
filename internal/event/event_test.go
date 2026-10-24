package event

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"
)

// 每种事件都要能"序列化 → 反序列化"还原，这是前后端协议一致的基础。
func TestRoundTripAllTypes(t *testing.T) {
	samples := []Payload{
		TurnStart{TurnID: "turn_1", Provider: "fake", Model: "demo"},
		TurnEnd{TurnID: "turn_1", Reason: TurnCompleted, Steps: 2, Usage: Usage{InputTokens: 1}},
		UserMessage{Text: "hi"},
		MessageStart{Provider: "fake", Model: "demo", Step: 1},
		TextDelta{Text: "你好"},
		ThinkingDelta{Text: "想一想"},
		Image{MediaType: "image/png", Data: "AAAA"},
		MessageEnd{StopReason: "end_turn", Usage: Usage{OutputTokens: 3}},
		ToolCall{CallID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"a"}`), Risk: "read"},
		ToolResult{CallID: "c1", Name: "read_file", Output: "ok"},
		PermissionRequest{RequestID: "p1", CallID: "c1", Tool: "write_file", Risk: "write", Input: json.RawMessage(`{}`)},
		PermissionResolved{RequestID: "p1", CallID: "c1", Decision: "allow"},
		SubagentStart{AgentID: "a2", ParentID: "main", Name: "explorer"},
		SubagentEnd{AgentID: "a2", ParentID: "main", Status: "completed"},
		TaskStart{TaskID: "t1", Title: "build"},
		TaskLog{TaskID: "t1", Stream: "stdout", Text: "..."},
		TaskEnd{TaskID: "t1", Status: "completed"},
		ContextCompacted{BeforeTokens: 100, AfterTokens: 10},
		Error{Code: "internal", Message: "boom", Fatal: true},
	}
	if len(samples) != len(AllTypes()) {
		t.Fatalf("样例数 %d 与已登记类型数 %d 不一致：新增事件类型后请补充样例", len(samples), len(AllTypes()))
	}

	bus := NewBus()
	for _, p := range samples {
		ev := bus.Publish(New("s1", "", "m1", p))
		b, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		got, err := Decode(b)
		if err != nil {
			t.Fatalf("%s: %v", ev.Type, err)
		}
		if !reflect.DeepEqual(got.Data, ev.Data) {
			t.Errorf("%s: data 不一致\n got  %#v\n want %#v", ev.Type, got.Data, ev.Data)
		}
		if got.Version != ProtocolVersion || got.AgentID != RootAgentID || got.Seq != ev.Seq {
			t.Errorf("%s: 信封字段不一致: %+v", ev.Type, got)
		}
	}
}

func TestDecodeUnknownType(t *testing.T) {
	_, err := Decode([]byte(`{"v":1,"type":"from_the_future","data":{}}`))
	if err == nil {
		t.Fatal("未知类型应返回错误")
	}
}

func TestBusOrderAndUnsubscribe(t *testing.T) {
	bus := NewBus()
	var a, b Recorder
	unA := bus.Subscribe(a.Handle)
	bus.Subscribe(b.Handle)

	bus.Publish(New("s", "", "", TextDelta{Text: "1"}))
	unA()
	bus.Publish(New("s", "", "", TextDelta{Text: "2"}))

	if n := len(a.Events()); n != 1 {
		t.Fatalf("取消订阅后不应再收到事件，got %d", n)
	}
	evs := b.Events()
	if len(evs) != 2 || evs[0].Seq != 1 || evs[1].Seq != 2 {
		t.Fatalf("序号应单调递增: %+v", evs)
	}
}

func TestBusConcurrentPublishSeqUnique(t *testing.T) {
	bus := NewBus()
	var rec Recorder
	bus.Subscribe(rec.Handle)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Publish(New("s", "", "", TextDelta{Text: "x"}))
		}()
	}
	wg.Wait()
	evs := rec.Events()
	for i, ev := range evs {
		// 同步回调 + 持锁发布：订阅者看到的顺序就是序号顺序。
		if ev.Seq != uint64(i+1) {
			t.Fatalf("第 %d 个事件序号为 %d", i, ev.Seq)
		}
	}
}
