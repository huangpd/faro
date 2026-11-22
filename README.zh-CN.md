# Faro

[English](README.md) · [简体中文](README.zh-CN.md)

一个原生的 macOS [sing-box](https://github.com/SagerNet/sing-box) 客户端。
Faro 是西班牙语"灯塔"。它是一个小巧的、GPU 渲染的应用，基于
[mygo](https://github.com/egoist/mygo) 界面框架：没有 WebView，不用 cgo。sing-box
作为独立进程运行，通过它的 Clash API 控制。

> 状态：早期（0.1.0）。仅 Apple Silicon，未做代码签名，暂无 TUN 模式。

## 它能做什么

- **订阅和节点。** 导入订阅链接或本地文件；节点按国家分组、自然排序、名字缩短。
  点一下节点就测速。
- **围绕出口，不是节点表。** 首页显示每类流量从哪里出去。一条路由可以固定在*一个*
  节点上，绝不自己切换；**熔断门**能立刻切断一条路由。路由所在的地区没有节点时会
  **阻断**，绝不换别的国家的节点。
- **你自己的域名，按分组存。** 给分组起名、选去向、添加域名。分组先于规则文本检查。
  在任何一条实时连接上，点一下就能把它的域名加入分组。
- **规则是一门自己的小语言**（不是 sing-box 或 Clash 的），报错带行号，页面同时显示
  它生成的配置。一个按钮可以复制一段提示词，让 AI 工具学会这门语言，帮你写规则；
  Faro 自己不会向任何地方发送内容。
- **每条连接都能解释自己。** 选中一条，就能看到命中了哪条规则、走了哪条路径。
- **DNS 分流。** 国内域名直接解析，国外域名通过代理解析，不使用系统的解析器。
- **社区规则集**（SagerNet 的 sing-geosite 和 sing-geoip）在后台保持最新，不需要
  重启核心。
- **轻量模式。** 窗口保持关闭，核心和菜单栏继续运行。一个窗口约占 150 MB，只留菜单栏
  约 20 MB。
- 菜单栏有网速、熔断门和节点；系统代理开关；可改的本地端口；深色和浅色外观。

## 规则语言

```
route work region DE gate      # 一条路由：德国节点，固定在其中一个上，带熔断门
work domain:example.com        # 一条规则：这个域名走那条路由
direct set:geosite-cn          # 中国大陆境内直连
```

去向可以是一条路由、`proxy`（主出口）、`auto`（最快的节点）、`direct` 或 `block`。
规则从上到下依次匹配，第一条命中的生效。运行应用，在"规则"页点"复制给 AI 的提示词"，
可以得到完整的语法说明。

## 构建

要求：macOS，Go 1.27 或更新。

```sh
./scripts/fetch-singbox.sh     # 下载固定版本的 sing-box 核心（开发和打包都用）
./scripts/fetch-rulesets.sh    # 下载随应用打包的规则集
go test ./...                  # 用真实的 sing-box 加假节点来测试
go run .                       # 从源码运行
./scripts/package.sh           # 构建 build/darwin-<arch>/faro.app 和磁盘镜像
```

代码结构：`internal/engine` 放 Faro 做的所有事（不含界面）；`internal/rules` 是规则语言；
`internal/singcfg` 生成 sing-box 配置；各个页面是根目录下的 `page_*.go`；`design-system/`
放设计说明和渲染预览图。

## 隐私

除了你让它转发的流量，Faro 只会连接：你添加的订阅链接、规则集镜像、你选的 DNS 服务器，
以及一个测速地址（`cp.cloudflare.com`）。没有遥测，没有账号。订阅数据保存在你自己 Mac
上的 `~/Library/Application Support/Faro`。

## 许可证

Faro 使用 MIT 许可证（见 [LICENSE](LICENSE)）。sing-box 是另一个独立的程序，有它自己的
许可证，由上面的脚本下载，作为独立进程运行；见
[resources/licenses/NOTICE.txt](resources/licenses/NOTICE.txt)，如果要分发包含它的构建，请先查看它的条款。
图标来自 [Lucide](https://lucide.dev)（ISC）。
