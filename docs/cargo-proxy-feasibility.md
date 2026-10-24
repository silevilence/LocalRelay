# Cargo registry 中转可行性评估

评估日期：2026-10-24。状态：官方协议与现有代码评估；尚未实现，未完成真实 Cargo 端到端验收。

## 结论与用户配置

可行。建议在现有资源代理端口增加只读 crates.io sparse registry 中转，覆盖依赖索引和 `.crate` 包下载。Cargo 官方支持 source replacement，适合原样镜像；不需要逐个修改项目依赖声明。[Source Replacement](https://doc.rust-lang.org/cargo/reference/source-replacement.html)

实现后，用户可在 `%USERPROFILE%\.cargo\config.toml`（自定义 `CARGO_HOME` 时为其下的 `config.toml`）加入：

```toml
[source.crates-io]
replace-with = "localrelay"

[source.localrelay]
registry = "sparse+http://127.0.0.1:8719/cargo/"
```

`sparse+` 前缀和末尾 `/` 应完整保留；局域网客户端换成 LocalRelay 所在机器地址。以上是拟议接口，当前版本尚不能使用。也可放到工作区根目录 `.cargo/config.toml`，但普通 `cargo install` 忽略项目本地配置，因此面向“配置一次即可使用”的指引应优先给全局位置。[配置发现规则](https://doc.rust-lang.org/cargo/reference/config.html#hierarchical-structure)、[cargo install 配置发现](https://doc.rust-lang.org/cargo/commands/cargo-install.html#configuration-discovery)

最低客户端基线建议 Cargo 1.68：该版本稳定了 sparse 协议。仅配置 `registry.default` 是选择命令使用的默认 registry，不能替代上述 crates.io 源替换。[Rust 1.68 公告](https://blog.rust-lang.org/2023/03/09/Rust-1.68.0/)、[registry.default](https://doc.rust-lang.org/cargo/reference/config.html#registrydefault)

## 最小服务设计

Cargo 首先读取索引根目录的 `config.json`，其中 `dl` 决定包下载位置。因此只代理索引会使包下载绕过 LocalRelay。官方索引配置目前使用 `https://static.crates.io/crates`；应把 `dl` 改成本地下载入口。[Registry Index](https://doc.rust-lang.org/cargo/reference/registry-index.html)、[官方 config.json](https://raw.githubusercontent.com/rust-lang/crates.io-index/master/config.json)

| 本地接口（建议） | 行为 |
| --- | --- |
| `/cargo/config.json` | 返回本地 `dl`，例如 `http://127.0.0.1:8719/cargo/crates/{crate}/{version}/download` |
| `/cargo/se/rd/serde` 等标准索引路径 | 转发到 HTTPS `index.crates.io`，保留索引原文 |
| `/cargo/crates/{crate}/{version}/download` | 转发到 HTTPS `static.crates.io/crates/{crate}/{version}/download`，流式返回原始包 |

索引记录含包的 SHA-256 校验和；保留索引及包字节即可保留 Cargo 原有校验。首版可省略可选 `api` 字段，明确只提供依赖读取和下载，不承诺搜索或发布。[索引与下载协议](https://doc.rust-lang.org/cargo/reference/registry-index.html)

实现建议：

- 只允许标准索引、配置与下载路径，校验包名、版本、方法和重定向；上游固定为上述两个主机。拒绝认证、发布及其他写接口，过滤客户端凭据与 Cookie。
- 复用当前流式下载、空闲超时、直连/代理切换与截断处理；Cargo 不需要额外运行时或整库同步。
- 索引和下载 CDN 的可达性缓存分别维护，避免一处失败影响另一处；与 GitHub/npm 的缓存同样隔离。
- `internal/githubproxy/server.go` 当前 `resource` 只包含名称、目标与重定向策略，npm 改写逻辑位于 `ServeHTTP`。增加轻量请求准备/响应处理钩子，或独立 Cargo handler，避免持续增加资源名称判断。
- `frontend/src/GitHubProxy.jsx` 增加 Cargo 配置示例与复制入口。复用现有端口、服务开关和出站配置；无须新增独立设置存储。

以上为结合当前代码的实现建议，不代表已有功能。

## 兼容范围与风险

| 场景 | 评估 |
| --- | --- |
| `cargo fetch/build/check/test` 的 crates.io 依赖 | 核心支持目标；构建脚本自行联网不属于 registry 中转 |
| 已有 crates.io `Cargo.lock` | 源替换机制适用，预期无需改写锁文件；必须用 `cargo fetch --locked` 并比较锁文件实测 |
| `cargo install` 的公开 registry 包 | 支持目标，配置必须放在其实际读取位置；`--git` 安装另属 Git 流量 |
| `cargo info` | 文档说明从包内 `Cargo.toml` 获取信息，可列入验收，不能简单等同于搜索 API |
| `cargo search` | 依赖 registry API，需要另增只读 API 转发与 registry 选择验证；首版建议明确不支持经本入口搜索 |
| Git 依赖、私有 registry、rustup 工具链 | 不属于此入口覆盖范围 |
| Cargo 1.68 之前 | 首版不兼容，避免为旧客户端引入全量 Git 索引机制 |

依据：[source replacement](https://doc.rust-lang.org/cargo/reference/source-replacement.html)、[cargo install](https://doc.rust-lang.org/cargo/commands/cargo-install.html)、[cargo info](https://doc.rust-lang.org/cargo/commands/cargo-info.html)、[cargo search](https://doc.rust-lang.org/cargo/commands/cargo-search.html)。锁文件保持不变为待验收预期，尚非本项目实测结论。

Cargo 会持久化索引并使用 ETag/Last-Modified 条件请求。普通索引应保留这些语义，防止新版本或撤回状态被陈旧缓存隐藏。改写的 `config.json` 不应复用上游校验器；本地地址由客户端实际访问地址生成，不能固定为环回地址。需单独验证缓存配置在端口、主机变化后的行为，不能仅凭 `Cache-Control: no-store` 就认定 Cargo 内部缓存已处理完毕。[Sparse 缓存机制](https://doc.rust-lang.org/cargo/reference/registry-index.html#caching)

## 实施与验收建议

工作量判断：中等，已有传输基础可复用，主要工作是协议路径、配置改写、缓存隔离和兼容验收。

首轮先完成索引与下载闭环，再补边界、UI 和文档。常规单元测试使用假上游，覆盖短包名分片、大小写、连字符/下划线、版本转义、路径穿越、重定向、304/404、凭据过滤和下载截断；公网测试另设 opt-in 开关，保持常规 `go test ./...` 不联网。

真实验收使用临时项目与独立 `CARGO_HOME`，检查索引和包请求都进入本地服务；验证新项目 fetch、已有锁文件 `fetch --locked`、全局配置下 install、支持版本下 info、重复请求缓存、局域网地址与端口切换，以及四种出站链路情形。索引/下载可达性故障分别注入。功能完成时同步 `docs/github-proxy.md`，不自动修改 ROADMAP 状态。
