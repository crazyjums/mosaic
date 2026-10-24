// Package sse 是一个极简的 Server-Sent Events 解析器，供各厂商适配器共用。
//
// # 负责什么
//
// 把 HTTP 响应体按 SSE 规范切分成一个个事件（event 名 + data 内容）。
// OpenAI 和 Anthropic 的流式接口都是 SSE，只是 event 名和 data 的 JSON 结构不同。
//
// # 不负责什么
//
// 不解析 data 里的 JSON，不理解任何厂商的事件含义——那是各适配器的事。
//
// 为什么自己写而不用第三方库：SSE 规范很小（按行读取、空行分隔、"field: value"），
// 自己实现不到 80 行，读一遍就能完全理解流式响应是怎么到达的，这正是学习项目想要的。
package sse
