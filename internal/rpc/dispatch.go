package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
)

// Methods 把一个对象（例如 *app.App）的公开方法变成可以按名字调用的表，供 Handler 使用。
//
// 调用约定和 Wails 的绑定一致，前端因此不用为 Electron 版改任何参数：
//   - params 是按位置的 JSON 数组，与 Go 方法的参数一一对应；少给的参数按零值处理，多给的报错；
//   - 返回值：() → null；(T) → T；(error) → error 非空时是错误回应；(T, error) → 同理。
type Methods struct {
	rcvr    reflect.Value
	methods map[string]reflect.Method
}

var errorType = reflect.TypeOf((*error)(nil)).Elem()

// NewMethods 收集 rcvr 的公开方法，跳过 exclude 里的（生命周期方法，不能让前端调用）和签名不支持的。
func NewMethods(rcvr any, exclude ...string) *Methods {
	skip := map[string]bool{}
	for _, n := range exclude {
		skip[n] = true
	}
	v := reflect.ValueOf(rcvr)
	t := v.Type()
	m := &Methods{rcvr: v, methods: map[string]reflect.Method{}}
	for i := 0; i < t.NumMethod(); i++ {
		meth := t.Method(i)
		if skip[meth.Name] || !supported(meth.Type) {
			continue
		}
		m.methods[meth.Name] = meth
	}
	return m
}

// supported：参数和返回值都要能 JSON 编解码（不能是函数、通道），最多两个返回值且第二个是 error。
func supported(ft reflect.Type) bool {
	for i := 1; i < ft.NumIn(); i++ { // 第 0 个是接收者
		if !jsonable(ft.In(i)) {
			return false
		}
	}
	switch ft.NumOut() {
	case 0:
		return true
	case 1:
		return ft.Out(0) == errorType || jsonable(ft.Out(0))
	case 2:
		return jsonable(ft.Out(0)) && ft.Out(1) == errorType
	}
	return false
}

func jsonable(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Func, reflect.Chan, reflect.UnsafePointer, reflect.Complex64, reflect.Complex128:
		return false
	}
	return true
}

// Names 返回所有可调用的方法名（排好序），握手时报告给对方，用作白名单。
func (m *Methods) Names() []string {
	out := make([]string, 0, len(m.methods))
	for n := range m.methods {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Has 判断有没有这个方法。
func (m *Methods) Has(name string) bool { _, ok := m.methods[name]; return ok }

// Call 按名字调用方法。
func (m *Methods) Call(_ context.Context, name string, params json.RawMessage) (any, error) {
	meth, ok := m.methods[name]
	if !ok {
		return nil, &Error{Code: CodeMethodNotFound, Message: "没有方法 " + name}
	}
	var raw []json.RawMessage
	if len(params) > 0 && string(params) != "null" {
		if err := json.Unmarshal(params, &raw); err != nil {
			return nil, &Error{Code: CodeInvalidParams, Message: name + "：params 必须是按位置的数组"}
		}
	}
	ft := meth.Type
	nIn := ft.NumIn() - 1
	if len(raw) > nIn {
		return nil, &Error{Code: CodeInvalidParams, Message: fmt.Sprintf("%s 只接受 %d 个参数，收到 %d 个", name, nIn, len(raw))}
	}
	args := []reflect.Value{m.rcvr}
	for i := 0; i < nIn; i++ {
		pt := ft.In(i + 1)
		pv := reflect.New(pt)
		if i < len(raw) && len(raw[i]) > 0 && string(raw[i]) != "null" {
			if err := json.Unmarshal(raw[i], pv.Interface()); err != nil {
				return nil, &Error{Code: CodeInvalidParams, Message: fmt.Sprintf("%s 的第 %d 个参数不对：%v", name, i+1, err)}
			}
		}
		args = append(args, pv.Elem())
	}
	out := meth.Func.Call(args)
	switch len(out) {
	case 0:
		return nil, nil
	case 1:
		if ft.Out(0) == errorType {
			return nil, asError(out[0])
		}
		return out[0].Interface(), nil
	default:
		if err := asError(out[1]); err != nil {
			return nil, err
		}
		return out[0].Interface(), nil
	}
}

func asError(v reflect.Value) error {
	if v.IsNil() {
		return nil
	}
	err := v.Interface().(error)
	var re *Error
	if errors.As(err, &re) {
		return re
	}
	return err
}
