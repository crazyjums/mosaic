// Package all 通过空导入把所有内置厂商适配器注册到 provider.DefaultRegistry。
//
// # 为什么需要这个包
//
// 各厂商子包在自己的 init() 里调用 provider.Register。Go 只有在包被导入时才会执行 init()，
// 所以必须有人导入它们。把所有空导入集中在这一个文件里，有两个好处：
//   - 入口（cmd/cli、Wails 的 main）只需要 `import _ ".../provider/all"`，不必知道有哪些厂商；
//   - 想知道"系统里注册了哪些厂商"，看这一个文件就够了。
//
// 新增厂商：新建子包并在 init() 中 Register，然后在 all.go 里加一行空导入。内核代码不需要任何修改。
package all
