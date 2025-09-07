<h2 align="center">IP 连通性检测 (IPv4/IPv6) · PING 检测 · IP是否被墙</h2>
<h5 align="center">国内腾讯云服务器 · 支持 IPv4/IPv6 · 页面与 API</h5>

---

## 8W2H（精简版）
- 为什么（Why）
  - 快速判断目标 IPv4/IPv6/域名是否可达，辅助排查是否被墙/丢包/仅单栈可用
- 是什么（What）
  - 一个 Go 服务：`/` 单页页面；`/api/ping` 文本；`/api/ping/json` JSON
- 面向谁（Who）
  - 运维/开发/测试，需要快速验证连通性的用户
- 何时用（When）
  - 日常排障、发布前连通性检查、双栈可用性验证
- 在哪用（Where）
  - 部署在中国大陆服务器（腾讯云等）或本地环境
- 怎么做（How）
  - 并发解析 A/AAAA → 对各族全部 IP 并行 ICMP Echo 竞速
  - ICMP 不通则并发 TCP(443/80) 竞速兜底 → 必要时回退系统 ping
  - 每次请求内多路并发、短时限，取“任一成功=ok”
- 做到什么程度（How well）
  - 在并发 100 请求下，服务器 CPU 占用约 5%（见下方截图），延迟低且稳定
- 多少成本（How much）
  - 单二进制部署，依赖极少；前端无第三方库

## 截图（Screenshots）
<div>
  <p>首页：</p>
  <img src="png/index-html-page.png" alt="index" width="640" />
</div>
<div>
  <p>压力测试（并发100/s，CPU≈5%）：</p>
  <img src="png/yali.png" alt="yali" width="640" />
</div>

## 运行（Run）
```
go run .
```
- 访问：`http://127.0.0.1:5601/`
- 端口：确保 5601 被放行（作为页面与 API 端口）

## API（Usage）
- 文本
```
GET /api/ping?ip=xxx
返回: text/plain
示例: ipv4:ok,ipv6:ok
```
- JSON
```
GET /api/ping/json?ip=xxx
返回: application/json
示例: {"code":200,"msg":"success","data":{"ipv4":"ok","ipv6":"ok"}}
```
- 说明：`ip` 支持 IPv4、IPv6、域名（域名并发解析 A/AAAA，并分别检测）

## 构建（Build）
- Windows 一键：`build.bat`（全平台交叉编译，彩色【Success】/【Error】）
- Linux/macOS 一键：`build.sh`（同功能同效果）
- 产物目录：`dist/`

## 部署（Deploy）
- Linux 原生 ICMP 建议授予：
```
sudo setcap cap_net_raw+ep /path/to/binary
```
- Windows 原生 ICMP 需管理员权限；否则自动回退系统 `ping`

## 常见问题（FAQ）
- 域名偶发 `no`？
  - 可能是 ICMP 被限速/墙或临时丢包；已加入 TCP 兜底与竞速，可按需提高超时
- 双栈不对称？
  - 某些域名仅一族解析或某族在当前网络不可达，表现为 `ok/no` 或 `no/ok`

## License  许可证
BSD开源协议
BSD开源协议是一个给于使用者很大自由的协议。基本上使用者可以”为所欲为”,可以自由的使用，修改源代码，也可以将修改后的代码作为开源或者专有软件再发布。

但”为所欲为”的前提当你发布使用了BSD协议的代码，或则以BSD协议代码为基础做二次开发自己的产品时，需要满足三个条件：

- 如果再发布的产品中包含源代码，则在源代码中必须带有原来代码中的BSD协议。
- 如果再发布的只是二进制类库/软件，则需要在类库/软件的文档和版权声明中包含原来代码中的BSD协议。
- 不可以用开源代码的作者/机构名字和原来产品的名字做市场推广。
BSD 代码鼓励代码共享，但需要尊重代码作者的著作权。BSD由于允许使用者修改和重新发布代码，也允许使用或在BSD代码上开发商业软件发布和销售，因此是对商业集成很友好的协议。而很多的公司企业在选用开源产品的时候都首选BSD协议，因为可以完全控制这些第三方的代码，在必要的时候可以修改或者二次开发。

任何使用、修改、分发、商业化等行为，都与作者无关，请自行承担风险 （就差无许可证写上去了）

## 免责声明（Disclaimer）
本项目仅用于技术交流与学习，不用于任何非法用途。
任何使用、修改、分发、商业化等行为，都需遵循 Apache License 2.0 协议

## 联系方式（Contact）
- 网站：https://golxc.com