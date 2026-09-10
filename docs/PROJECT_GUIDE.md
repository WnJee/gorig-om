# Gorig-OM 项目指南（架构 · 二开 · 维护）

> 本文档面向二次开发与维护人员，基于源码分析整理（go.mod: `github.com/WnJee/gorig-om`，Go 1.23，gorig v0.0.53-0.20260205102704-ca4d73b27ac3）。README 介绍"怎么用"，本文介绍"怎么改、怎么维护"。

---

## 1. 项目定位

Gorig-OM 是 [Gorig](https://github.com/jom-io/gorig) 框架的**运维管理后端**（Operations & Maintenance），以 Go 库形式发布。宿主 Gorig 应用只需 import 本包，即可获得一组挂在 `/om/**` 前缀下的运维 HTTP 接口：

- 应用进程重启 / 停止 / 看门狗（watchdog）
- Git + Go 环境检测安装、SSH Key 管理、Go env 配置
- CI/CD 部署流水线（拉代码 → 构建 → 重启 → 回滚）
- 日志检索（JSONL 文件全文检索、上下文查看、SSE 实时监控、下载）
- 统计监控：API 延迟、错误签名、协程趋势、内存大对象 / 泄漏检测
- 主机资源采集（CPU / 内存 / 磁盘）

前端面板独立部署：<https://jom-io.github.io/gorig-om>（基于 Slash Admin）。

### 总体形态

```
宿主 Gorig 应用 (bootstrap.StartUp())
        │  main() 中显式调用 om.Setup()
        ▼
Setup()（sync.Once 幂等）
        │  variable.OMKey 为空则直接退出（功能关闭）
        ├── host.Init() / apistat.Init() / errstat.Init()
        │   / gorstat.Init() / memstat.Init() / task.Init()
        │   （注册定时采集与后台 goroutine）
        ▼
httpx.RegisterRouter() 挂载 /om 路由组 ──► mid.Sign() JWT 鉴权
        │
        ├── deploy/app   进程重启/停止/watchdog 脚本生成器（无状态，无需 Init）
        ├── deploy/env   git/go 环境、SSH key（无状态，无需 Init）
        ├── deploy/task  部署流水线（后台 goroutine + cronx 定时器）
        ├── host         主机资源采集（cronx 每分钟）
        ├── logtool      JSONL 日志检索引擎
        └── stat/*       apistat / errstat / gorstat / memstat
                │
                ▼
     gorig/cache（Sqlite 后端 Pager/KV 存储）+ 工作目录文件（.logs/.cache/.deploy）
```

**关键点：OM 不是独立服务**，它寄生在宿主应用进程内；所有数据默认落在宿主应用工作目录的 SQLite 与日志文件中。

---

## 2. 技术栈与核心依赖

| 依赖 | 用途 |
|---|---|
| `github.com/jom-io/gorig` | 主框架：bootstrap/httpx/apix/cache/cronx/messagex/tokenx/logger/errors |
| `gin-gonic/gin` | HTTP 路由（经 gorig httpx 封装） |
| `shirou/gopsutil/v4` | 主机 CPU/内存/磁盘/进程指标采集 |
| `google/pprof` (`profile`) | 解析 heap profile，用于内存大对象与泄漏 diff |
| `fsnotify` | 日志文件监听（SSE 实时 tail） |
| `tidwall/gjson` | JSONL 行级快速解析 |
| `rs/xid` | 任务 ID / startID 生成 |
| `golang.org/x/crypto/bcrypt` | 登录口令校验 |
| `spf13/cast`、`uber/zap` | 类型转换、结构化日志 |

gorig 框架侧的关键设施（二开必知）：

- **`httpx.RegisterRouter(func(*gin.RouterGroup))`**：向宿主应用注册路由。
- **`apix`**：控制器工具。约定每个 controller 第一行 `defer apix.HandlePanic(ctx)`；取参用 `apix.GetParamType[T] / GetParamStr / GetParamInt64 / GetParamForce / GetParamArray[T] / BindParams / Bind / GetPageReq`；响应统一 `apix.HandleData(ctx, consts.CurdXxxFailCode, data, err)` 信封。
- **`cache.Pager[T]`（Sqlite 后端）**：带分页/聚合的记录存储。常用方法 `Put / Get(cond) / Update(cond, v) / Delete(cond) / Count / Find(page,size,cond,sorter) / GroupByTime / GroupByFields`。条件语法是 Mongo 风格 map：`{"at": {"$gte": x, "$lt": y}}`、`{"$in"/"$nin"/"$ne"/"$like": ...}`、`"$having": "sql片段"`。
- **`cache.Cache[T]`**：KV 缓存，`cache.JSON`（内存）或 `cache.Sqlite`（落盘）。
- **`cronx.AddTask / AddCronTask(spec, fn, delay)`**：定时任务。
- **`messagex`**：进程内 pub/sub，部署完成事件用它解耦。
- **`tokenx.Get(tokenx.Jwt, tokenx.Memory)`**：JWT 生成与校验（Memory 存储会话）。

---

## 3. 目录结构

```
src/
├── router.go            # 唯一入口：Setup() 编排各模块 Init + 注册全部路由（唯一路由清单）
                         # 全项目禁止 init()，宿主 main 显式调用 om.Setup()
├── omuser/login.go      # 登录鉴权（bcrypt+时间窗防重放、错误锁定、JWT 签发）
├── mid/sign.go          # Sign() 中间件：Bearer token / ?token= 校验 + IsOM 判定
│
├── deploy/
│   ├── command.go       # RunCommand：统一命令执行器（nice 包装、超时、超时消息广播）
│   ├── topic.go         # TopicRunStarted = "run.started"
│   ├── app/             # 进程生命周期：restart.sh / stop.sh / watchdog 脚本生成与执行
│   │   ├── api.go       #   Restart / ReStared / Stop / RestartLogs 控制器
│   │   ├── app.go       #   核心逻辑（脚本模板、startID 校验、看门狗启动）
│   │   └── model.go     #   StartSrc 枚举、ReStartLog（Sqlite 分页存储）
│   ├── env/             # git/go 环境检测安装、分支列表、SSH key、GoEnv 读写
│   └── task/            # 部署流水线（注意：包名是 delpoy）
│       ├── model.go     #   TaskOptions / TaskRecord（含运行日志追加 Running()/TimeOut()）
│       ├── task.go      #   clone→buildFile→app.Restart 全流程、autoCheck、超时清理
│       └── api.go
├── host/                # 主机资源：每分钟采集 ResUsage 入 Sqlite，支持分页/时间聚合
├── logtool/             # 日志引擎
│   ├── search.go        #   SearchLogs / MonitorLogs(SSE+fsnotify) / FetchContextLines /
│   │                    #   DownloadLogs / readLastRecord（倒序读尾）
│   ├── options.go       #   SearchOptions / Level 枚举 / MatchedRecord
│   └── model.go         #   LogRecord（zap json 字段映射：level/time/_trace_id_/msg/error）
└── stat/
    ├── apistat/         # API 延迟统计：分钟桶 + 小时 rollup + Top 排名 + 样本
    ├── errstat/         # 错误计数 + 错误签名归并（归一化→sha1）
    ├── gorstat/         # 协程数采样（30s 一次）
    └── memstat/         # 内存：heap profile 大对象 Top50 + GC 窗口泄漏检测

simple/main.go           # 最小宿主示例（om.Setup() + bootstrap.StartUp）
test/                    # 集成测试（apistat/host/logtool/deploy task 等）
```

---

## 4. 启动与初始化流程

**全项目没有任何 `init()`**。所有初始化收拢在根包唯一入口 `src/router.go` 的 `Setup()` 中，由宿主 main 显式调用：

```go
func main() {
    om.Setup()            // 必须在 bootstrap.StartUp() 前后均可，但建议先调
    bootstrap.StartUp()
}
```

1. `Setup()` 用 `sync.Once` 保证幂等，可安全重复调用。
2. 检查 `variable.OMKey`（来自配置 `om.key`）：为空则整个 OM 不启用——不注册路由、不启动任何定时任务与后台 goroutine。
3. 依次调用各模块 `Init()`：
   - `host.Init()`：解析 `om.host.max_period`；每分钟 `Collect`；goroutine 每分钟清理过期数据。
   - `apistat.Init()`：第 45 秒 `Collect`（扫描上一分钟 rest/invoke 日志）；goroutine 每分钟 rollup+清理。
   - `errstat.Init()`：第 30 秒 `Collect`；goroutine 每分钟清理。
   - `gorstat.Init()`：每 30s 采样协程数；goroutine 每分钟清理。
   - `memstat.Init()`：加载 `om.stat.mem.*` 配置；`baselineLoop`（默认 5 分钟一次 heap profile → 大对象 Top50）+ `leakLoop`（10 秒一次 GC 窗口泄漏检测）。
   - `task.Init()`：每 10s `autoCheck`、每 10s 超时检查、每 10 分钟备份清理；goroutine 每 5s 扫描 Waiting 任务、订阅 `run.started` 事件。
4. 注册 `/om` 路由组；其中 `/om/app/restarted` 与 `/om/auth/connect` 在 `mid.Sign()` 之前注册（免鉴权），其余全部走签名中间件。

> `deploy/app` 与 `deploy/env` 无后台状态，不需要 Init；app 的 watchdog 文件名为惰性计算（调用时读取 SysName/RunMode）。

---

## 5. 认证机制（omuser + mid）

登录流程 `POST /om/auth/connect`，参数 `pwd`：

1. 服务端构造明文 `localPwd = fmt.Sprintf("%d%s", time.Now().Unix()/10, variable.OMKey)`（10 秒时间窗）。
2. 校验客户端传来的 bcrypt hash 是否匹配该明文 —— 即**前端先对 `Unix时间戳/10 + om.key` 做 bcrypt 再提交**，实现防重放（每次窗口不同）。
3. 防爆破：按 `OM-<ClientIP>` 记失败次数于 `loginErrCount`（JSON 内存缓存），5 次失败锁 10 分钟。
4. 通过后 `tokenx(Jwt, Memory)` 签发 1 小时 token；userID 为 `OM-<IP>`。

`mid.Sign()` 中间件：

- 取 `Authorization: Bearer <jwt>` 或 query `?token=`；
- 解析 JWT 且要求会话存在（`Manager.GetUserID`）；
- `omuser.IsOM(userID)` 要求 userID 前缀为 `OM`，否则 403 —— 保证普通业务 token 不能访问 OM 接口。

> 二开提示：换认证方式只动 `omuser/login.go` + `mid/sign.go` 两处；新增免鉴权接口必须在 `Setup()` 中把路由注册放在对应 `Use(mid.Sign())` 之前。

---

## 6. 模块详解

### 6.1 deploy/command.go — 命令执行器

全项目唯一的 shell 出口（除 env.trustHost 直接 exec ssh-keyscan 外）：

- 所有命令通过 `nice -n <N>` 包装执行（默认 Nice=5，范围 -20~19）；
- `RunOpts`: Dir / Env / PrintLog / TimeOut（默认 1min）/ Nice；
- 超时通过 context 控制，超时会向 `run_timeout.<traceID>` topic 发 messagex 消息（task 模块订阅它来标记任务超时）;
- stderr 有内容时返回错误（含 stderr 文本），无 stderr 但 exit≠0 时返回空串 + nil（**调用方需自行判断输出是否符合预期**，如 CheckGit 靠解析输出来判断安装状态）。

### 6.2 deploy/app — 进程重启 / watchdog

约定：宿主应用的可执行文件名必须为 `<sysname>-<runmode>.linux64`（小写、下划线转连字符），放在应用工作目录。

`Restart(ctx, runFile, runBack, itemIDs...)` 流程：

1. 生成 `startID`（xid）写入缓存；
2. 动态生成 `restart.sh`：
   - `pkill -15 -f <runFile>`，10s 内未退再 `pkill -9`；
   - `nohup ./<runFile> > nohup.out`，写 `app.pid`；
   - 循环 curl `http://127.0.0.1<api.rest.addr>/om/app/restarted?startID=...&itemID=...&pid=...&src=<src>` 直到 HTTP 200（最长 120s）。
3. 动态生成 watchdog 脚本 `watchdog_<sysname>_<runmode>.sh`：
   - 每 5s 检查进程存活（app.pid 或 pgrep），崩溃则备份 nohup.out 到 `restart_logs/` 并以 `crash` 来源重启；
   - CPU/内存超阈值连续 3 次（proc>50% 且 sys>90%）以 `overuse` 来源重启（30s 冷却）。
4. 杀掉旧 watchdog，后台执行 restart.sh（来源 src：manual/deploy）。

新进程启动后回调 `GET /om/app/restarted` → `RestartSuccess()`：比对 startID 一致则删缓存并启动 watchdog；不一致/缺失走 fallback 也启动 watchdog（幂等，pgrep 去重）。同时异步读取 `restart.log`（及 crash 场景的 restart_logs 最新文件尾部 300 行）存入 `ReStartLog` 表。

`Stop`：生成 stop.sh（pkill watchdog + pkill 应用 + 删 app.pid）后台执行。

### 6.3 deploy/env — Git / Go 环境

| 能力 | 说明 |
|---|---|
| CheckGit / Install | `git --version`；缺失时探测 apt/yum/apk 自动安装 |
| Branches(repoUrl) | `git ls-remote --heads`；Host key 校验失败时自动 `ssh-keyscan` 信任主机后重试 |
| GetSSHKey / GenSSHKey | 读/生成 `~/.ssh/id_rsa(.pub)`（4096 RSA，comment `gorig@hostname`） |
| GetLatestHash(repo,branch) | 远端最新 commit hash（autoCheck 用于判断是否有新版本） |
| CheckGo / InitGo | 要求 ≥ GOVersion(1.23.4)；缺失时从 dl.google.com 下载 linux-amd64 包装到 /usr/local/go |
| GoEnvGet/Set | GoEnv 列表存 Sqlite KV（key=`go_env`）；默认强制注入 GOARCH=amd64、GOOS=linux；Set 会 diff 出被删除的非默认项并 `os.Unsetenv` + `go env -u` |

### 6.4 deploy/task — 部署流水线（核心）

配置 `TaskOptions`（存 Sqlite KV，key=`dp_task_config`）：repo/branch/gitInit/goInit/sshKeyCopy/otherRepos/autoTrigger。

任务记录 `TaskRecord`（Sqlite 分页表）：状态机 `waiting → running → success/failed/timeout/canceled`；回滚位 `RBStatus: "" → ready → cleaned`；`RB/RID` 标记回滚任务及其源任务。`Running(log, level)` 方法既追加运行日志又负责状态推进（Error 日志直接置 failed 并 finish；发现 DB 里已是 canceled/timeout 则中止本地流程）。

**执行链**（`deploy(ctx)`，5s 轮询，单并发：存在 running 任务则跳过）：

1. `clone`：清空 `.deploy/code` → `git clone --depth 1 -b <branch>` 主仓库与 OtherRepos 到 `.deploy/code/{main,<dir>}` → 读最新 commit message。
2. `buildFile`：
   - 回滚任务(RB)：直接复制备份产物 `<name>_<ts>.linux64`；
   - 正常构建：读 GoEnv（GOPROXY 未配置时探测 proxy.golang.org，不通自动切 goproxy.cn）→ `go env -w` 逐条写入 → 准备 ssh known_hosts、`git config insteadOf https→git@` → `GOMAXPROCS=N-1` 下 `go mod tidy`（5min 超时）→ Walk 找第一个 `main.go` → `go build -o <outputName> -ldflags "-w -s" -trimpath <main.go>`（5min 超时）→ 复制到工作目录 + 备份到 `.deploy/build/<name>_<ts>.linux64`。
3. `app.App.Restart(ctx, runFile, runBack, item.ID)`：复用 6.2 的重启流程，`itemID` 用于事件关联。
4. 新进程起来后 `restarted` 回调 publish `run.started{itemID,pid}` → `StartedListen` 收到后把任务置 success、RBStatus=ready（可回滚）。
5. 兜底：cron 每 10s 把 running 超 10 分钟的任务标 timeout；每 10 分钟 `CleanBackup` 只保留最近 10 个备份并把引用它们的任务 RBStatus 置 cleaned。

`Rollback(id)`：基于原任务的 BuildFile 创建新的 waiting 任务（RB=true, RID=原ID），走同一条流水线但跳过 clone/build。

### 6.5 host — 主机资源

- 每分钟 `Collect`：并行采 host CPU（1s 窗口均值）与本进程 CPU%；RSS 内存、根分区用量、当前目录递归大小（AppDisk）；全部格式化为字符串字段存 `ResUsage`。
- 查询：`Page`（倒序分页）、`TimeRange(start,end,unit,filter)`（GroupByTime + AggAvg，unit 支持 day 等 granularity）。
- 清理：`om.host.max_period`（默认 720h）之外的记录每小时删一次。

### 6.6 logtool — 日志引擎（多数统计模块的地基）

数据源约定：工作目录下 `.logs/<category>/<category>*.jsonl`，每行一条 zap JSON 日志（字段 `level/time/_trace_id_/msg/error/...`）。

- `FetchCategories`：列出含 .jsonl 的子目录。
- **文件时间边界缓存**：`readLogTimeBounds` 按 `(size, mtime)` 记忆每个文件的 [首条时间, 末条时间]，文件未变化时直接命中缓存——apistat/errstat 每分钟重复扫描时不再反复解析历史文件的首尾行（缓存上限 4096 条，超出整体重置）。
- `SearchLogs(SearchOptions)`：
  - 过滤维度：categories、level(s)、traceID、keyword（msg/error/data 值包含匹配）、startTime/endTime（秒级截断到毫秒比较）；
  - 两级过滤：`preFilter` 对原始行做字符串包含快速过滤（`"level":"xxx"` 等），`postFilter` 对解析后的记录精确判断；
  - 游标续传：`LastPath/LastLine` 从指定文件行之后继续；
  - traceID 优化：xid 可解析时自动把搜索窗收敛到创建时间 ±1h；
  - 单行上限 1MB，超长行跳过剩余字节。
- `MonitorLogs`：SSE。fsnotify 监听匹配文件 Write 事件，每次取文件最后一条完整记录（`readLastRecord` 块状倒序读）匹配后推送。
- `FetchContextLines(path,line,range)`：返回某行前后 range 行原文+解析结果。
- `DownloadLogs(path)`：仅允许 `.jsonl`，且路径解析后必须位于日志根目录 `<workdir>/.logs/` 内（防目录穿越 / 任意文件读取）。

### 6.7 stat/apistat — API 延迟统计

- 采集（每分钟）：搜上一分钟 `rest`/`invoke` 类目日志，按 `_trace_id_` 配对 msg=IN/OUT 的记录，latency=out.at-in.at；URI 规范化去 query string；按 method|uri 聚合分钟桶（count/slow(>200ms)/sum/max 按 2xx/4xx/5xx/other 分类）；同时在 `api_latency_meta` 维护每个接口的最新/最快慢样本。
- Rollup：每小时把上一小时的分钟表聚合成小时表（`api_latency_stat_hour`），分钟表只保留当前小时。
- 查询：
  - `TimeRange`：GroupByTime AggSum（默认排除 OPTIONS/HEAD/PATCH）；
  - `Summary`：总数/平均延迟(2xx)/5xx 数/slow 数（slowMs≠200 时回退重新扫日志统计）；
  - `TopPage`：排序支持 avg/max/count/2xx/4xx/5xx/other/success（SQL 表达式 `sum/NULLIF(cnt,0)`）；跨"历史小时表+当前分钟表"两段合并；statuses/having 过滤；
  - `Sample`：按 method+uri 返回 latest/2xx/4xx/5xx/slow 样本（含出入参日志摘要）。

### 6.8 stat/errstat — 错误统计与签名

- 每分钟统计 error/fatal/dpanic 日志数 → `ErrStat{warn,error,panic,total}`。
- 错误签名：`normalizeText` 把 UUID/十六进制/数字替换为 `?`、压缩空白 → `msg | error` 拼接 → `sha1(level|signature)`；分钟粒度计数入 `err_sig_stat`，元信息（样例、首末次出现）入 `err_sig_meta`。
- `TopSignatures`：时间窗内 GroupByFields(sigHash) sum(count) desc，回填 meta。
- 清理周期配置 `om.stat.err.max_period`（默认 720h）。

### 6.9 stat/gorstat — 协程趋势

30s 采样 `runtime.NumGoroutine()`（只读运行时计数器，不 STW，开销可忽略）；查询为 GroupByTime AggAvg 四舍五入。清理配置 `om.stat.goroutine.max_period`；总开关 `om.stat.runtime.enabled`（与 memstat 共用，默认 true）。

### 6.10 stat/memstat — 内存诊断

阈值与节奏可通过配置调整（serv.go `loadMemConfig`，二开调参入口）：

| 配置键 | 默认 | 说明 |
|---|---|---|
| `om.stat.runtime.enabled` | true | 运行时监控总开关：同时控制大对象采样、泄漏检测与协程数采集（gorstat），关闭需重启生效 |
| `om.stat.mem.big_sample_interval` | 5m | heap profile 大对象采样间隔 |
| `om.stat.mem.leak_alloc_delta_mb` | 100 | 触发泄漏捕获的 HeapAlloc 窗口增量下限（MB） |
| `om.stat.mem.leak_object_delta` | 100000 | 触发泄漏捕获的 HeapObjects 窗口增量下限 |
| `om.stat.mem.leak_cooldown` | 2m | 两次泄漏捕获的最小冷却时间 |

高吞吐服务误报时可调大阈值；对延迟敏感的服务可用总开关一并关闭——**大对象采样与泄漏检测均有周期性 STW 开销**：大对象采样每次 `pprof.WriteHeapProfile` 快照堆会短暂 STW 并序列化全量样本（大堆服务上单次可达几十~几百 ms），泄漏检测每 10s 的 `runtime.ReadMemStats` 同样会短暂 STW。

- **大对象**：按采样间隔 `pprof.WriteHeapProfile` 到 `.cache/heap/heap_base_*.pprof`，解析 inuse_space/inuse_objects 按函数位置聚合，Top50 且 ≥1MB 入 `mem_big_stat`；base profile 只留 1 份。
- **泄漏检测**：每 10s 读 MemStats，GC 次数前进才采样；滑动窗口 5 次 GC 样本（最小间隔 1min），HeapAlloc 或 HeapObjects 增量达到阈值 → 触发泄漏捕获：写 `heap_leak_*.pprof`，与 base profile diff 出增长 Top10 函数，连同 profile 总量差值存 `mem_leak_event`（leak profile 留 100 份/7 天，事件留 1 万条）。API 层返回时会抹掉 profile 路径字段。
- **测试钩子**：环境变量 `MEMSTAT_LEAK_TEST=1` 时 init 启动人为泄漏场景（bulk/small/strings/map/slow，均可用 MEMSTAT_LEAK_* 变量调参）；`MEMSTAT_FORCE_GC=1` 让写 profile 前 GC。**生产环境切勿开启**。

---

## 7. API 接口清单

除注明外均需 Bearer token。统一响应信封 `apix.HandleData`（code/data/msg）。

### 免鉴权
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/om/auth/connect` | body/query `pwd`（bcrypt(Unix/10 + om.key)）→ token |
| GET | `/om/app/restarted` | restart.sh 内部回环回调：startID/itemID/src/pid（Sign 中间件对该路由不生效） |

### 应用
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/om/app/restart` | 生成并执行 restart.sh |
| POST | `/om/app/stop` | 生成并执行 stop.sh |
| GET | `/om/app/restart/logs?page&size` | 重启历史（ReStartLog 分页） |

### 日志
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/om/log/categories` | `.logs` 下类目列表 |
| GET | `/om/log/levels` | 支持的日志级别枚举 |
| POST | `/om/log/search` | body=SearchOptions，返回 MatchedRecord[]（path/line/record） |
| GET | `/om/log/near?path&line&range` | 某行上下文 |
| GET | `/om/log/monitor?...` | SSE 实时推送（参数同 search） |
| GET | `/om/log/download?path` | 下载 .jsonl |

### 部署
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/om/deploy/git/check` · POST `install` | git 检测 / 安装 |
| GET | `/om/deploy/branches?repoUrl` | 远端分支列表 |
| GET/POST | `/om/deploy/ssh/key` | 读取 / 生成 SSH key |
| GET/POST | `/om/deploy/go/check`·`install` | go 检测（≥1.23.4）/ 安装 |
| GET/POST | `/om/deploy/go/env` | GoEnv 读 / 写 |
| GET/POST | `/om/deploy/task/config` | TaskOptions 读 / 写 |
| POST | `/om/deploy/task/start` | 手动创建 waiting 任务 |
| POST | `/om/deploy/task/stop?id` | 取消 waiting/running 任务 |
| GET | `/om/deploy/task/page?page&size` · `get?id` | 任务列表 / 详情（含运行日志） |
| POST | `/om/deploy/task/rollback?id` | 基于历史产物创建回滚任务 |

### 主机
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/om/host/usage?page&size` | 资源快照分页 |
| GET | `/om/host/usage/time?start&end&unit&filter` | 时间聚合（AggAvg；filter=cpu,appCpu,mem,...） |

### 统计
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/om/stat/error/time?start&end&unit&filter` | 错误趋势（warn/error/panic/total） |
| GET | `/om/stat/error/top?start&end&filter&limit` | 错误签名 Top |
| GET | `/om/stat/api/time?start&end&unit&filter` | API 请求数趋势 |
| GET | `/om/stat/api/summary?start&end&slowMs` | 汇总卡片 |
| GET | `/om/stat/api/top?...` | 接口排行（methods/negMethods/uriPrefix/uriLike/statuses/sortBy/asc/page/size） |
| GET | `/om/stat/api/sample?method&uri&types` | 接口请求样本 |
| GET | `/om/stat/goroutine/time?start&end&unit` | 协程趋势 |
| GET | `/om/stat/mem/big/top?start&end&page&size&sortBy&asc` · `big/count` | 大对象排行 / 数量 |
| GET | `/om/stat/mem/leak/latest` · `leak/count?start&end` · `leak/page` | 泄漏事件 |

---

## 8. 数据与文件布局

### Sqlite 表（gorig cache Pager，库名由框架决定）

| 存储 key | 内容 | 保留策略 |
|---|---|---|
| （默认表）ResUsage / ErrStat / ReStartLog | 主机资源、错误分钟统计、重启日志 | host 720h / err 720h |
| `api_latency_stat` / `api_latency_stat_hour` | 分钟桶 / 小时 rollup | 分钟只留当小时；小时 30 天 |
| `api_latency_meta` | 接口样本元信息 | lastAt 30 天前删除 |
| `err_sig_stat` / `err_sig_meta` | 错误签名 | 720h |
| `goroutine_stat` | 协程采样 | 720h |
| `mem_big_stat` / `mem_leak_event` | 大对象 / 泄漏事件 | profile 文件 7 天；事件 1 万条 |
| `client_day_stat`（+meta cursor） | 客户端日统计 | 90 天（模块停用中） |

### KV（cache.Cache）
- Sqlite：`dp_task_config`（部署配置）、`go_env`
- JSON（内存）：`loginErrCount`、`startID`

### 运行时产生的文件（均在宿主应用工作目录）

```
restart.sh / stop.sh / watchdog_<sys>_<mode>.sh   # 动态生成的脚本（0755）
nohup.out / app.pid / restart.log / watchdog.out
restart_logs/                                     # 崩溃自动重启时的日志快照
.deploy/code/  .deploy/build/                     # 部署工作区与构建备份
.cache/heap/*.pprof                               # heap profiles（0700）
.logs/<cat>/*.jsonl                               # 日志（logtool 的数据源）
```

---

## 9. 配置项速查

```yaml
om:
  key: "<access-key>"          # 必填才启用 OM（variable.OMKey）
  host:
    max_period: 720h           # 主机资源保留时长
  stat:
    err:
      max_period: 720h
    runtime:
      enabled: true               # 运行时监控总开关：协程数 + 大对象 + 泄漏检测
    goroutine:
      max_period: 720h
    mem:
      big_sample_interval: 5m     # 大对象采样间隔
      leak_alloc_delta_mb: 100    # 泄漏触发：HeapAlloc 窗口增量下限（MB）
      leak_object_delta: 100000   # 泄漏触发：HeapObjects 窗口增量下限
      leak_cooldown: 2m           # 泄漏捕获冷却时间
api:
  rest:
    addr: ":9617"              # restart.sh 回调地址取此配置
```

环境变量：`GORIG_SYS_MODE`（restart.sh 注入）、`MEMSTAT_LEAK_TEST` 系列、`MEMSTAT_FORCE_GC`。

---

## 10. 二次开发指南

### 新增一个 OM 接口（推荐姿势）

1. 建目录 `src/<module>/`，按现有惯例三件套：`model.go`（结构体）、`serv.go`/业务实现、`api.go`（controller）。
2. Controller 模板（务必遵守，保持一致）：

```go
func Xxx(ctx *gin.Context) {
    defer apix.HandlePanic(ctx)
    param, e := apix.GetParamStr(ctx, "param", apix.Force)
    if e != nil { return }
    result, err := Serv.Xxx(ctx, param)
    apix.HandleData(ctx, consts.CurdSelectFailCode, result, err)
}
```

3. 业务层持久化优先用 `cache.NewPager[T](ctx, cache.Sqlite[, tableName])`；时间序列查询直接用 `GroupByTime / GroupByFields`（参考 gorstat 最简、apistat 最全）。
4. 在 `src/router.go` 的 `Setup()` 中把路由加进对应分组（默认就在 `om.Use(mid.Sign())` 之后，自动受保护）。
5. 若需要定时采集：模块提供导出的 `Init()`（内部先判断 `variable.OMKey == ""` 早退，再 `cronx.AddCronTask(...)` + 每分钟清理 goroutine + `max_period` 配置项），并在 `src/router.go` 的 `Setup()` 中挂载。**不要写 `init()`**——初始化一律由 Setup 统一调度。
6. **参数解析约定**：`apix.GetParam*` 系列的返回错误一律用 `_` 丢弃、不做判断——非强制参数缺参时框架直接返回默认值；强制（Force）参数缺参时框架已自动写 400 并 abort，且 `ReturnJson` 内部有 `Writer.Written()` 防重入保护，后续 `HandleData` 不会二次写响应。**必传性校验放在业务层**（对零值/空值返回业务错误）。仅结构体绑定 `BindParams / Bind` 保留判错提前 return（绑定失败不应继续执行业务逻辑）。

### 新增一个统计模块 checklist

- [ ] 数据结构带 `At int64 \`json:"at"\`` 时间戳字段（GroupByTime 依赖 at 字段）
- [ ] Collect 定时器 + SearchLogs/系统指标取数
- [ ] Clear 清理 + `om.stat.<name>.max_period` 配置
- [ ] TimeRange/Top 类查询走 GroupByTime/GroupByFields，勿在内存里全量过滤
- [ ] test/ 下补集成测试（参照 apistat_test.go 的 TempDir+chdir 模式隔离工作目录）

### 修改部署流水线的注意点

- `deploy/task` 的**包名是 `delpoy`**（拼写遗留），import 时路径是 `src/deploy/task` 但包标识符写作 `delpoy`，改名属破坏性变更需同步 router.go/test。
- 任务状态推进的唯一入口是 `TaskRecord.Running()`，不要在流水线里绕过它直接改 Status（会丢失取消/超时仲裁逻辑）。
- 构建产物命名 `<sysname>-<runmode>.linux64` 被 app.Restart 的 getRunFileName 硬编码依赖，改动需两处同步。
- RunCommand 默认 1 分钟超时，clone/tidy/build 都显式放大过，新步骤记得评估超时。

### 版本升级 / 依赖

- gorig 使用的是伪版本 `v0.0.53-0.20260205102704-ca4d73b27ac3`（非 tag）。升级时用 `go get github.com/jom-io/gorig@<commit>`，然后核对 apix/cache/cronx/tokenx 签名是否变化。
- 验证命令：`go build ./... && go vet ./src/...`；测试在 `test/`，跑法 `go test ./test/ -run TestApiStatWorkflow` 等（部分测试依赖真实环境：dp_git/dp_task 需要 git 与网络，host_test 会真实采集 15s）。

---

## 11. 已知问题与维护注意事项

### 已修复（2026-08）

1. ~~**统计模块不受 om.key 控制**~~ → 已修复（并随初始化收拢重构）：全项目无 `init()`，所有模块由 `om.Setup()` 统一编排，`om.key` 未配置时不注册路由、不启动任何定时采集与后台 goroutine，OM 完全静默。
2. ~~**`Clean()` 与实际 watchdog 文件名不匹配**~~ → 已修复：`Clean` 改为直接引用包内 `watchdogFile` 变量，与生成名一致。
3. ~~**`host.Host()` 非严格单例**~~ → 已修复：改用 `sync.Once` 惰性初始化并复用同一实例。
4. ~~**控制器参数吞错**~~ → 已按新约定重构：全部控制器 `apix.GetParam*` 解析错误统一 `_` 丢弃（缺必传参由框架自动回 400 并 abort），必传性校验收敛到业务层（如 task 的空 id 校验、login 的空 pwd 校验、env.Branches 的空 repo 校验）；结构体绑定 `BindParams/Bind` 仍保留判错。
8. ~~**logtool 重复扫描开销**~~ → 已缓解：文件时间边界按 `(size, mtime)` 缓存（上限 4096 条），apistat/errstat 周期扫描不再重复解析未变化文件的首尾行。逐行扫描本身仍是 O(日志量)，超大日志量场景仍建议缩小类目或拉长间隔。
9. ~~**`DownloadLogs` 路径防护有限**~~ → 已修复：下载路径经绝对化后强制限定在 `<workdir>/.logs/` 内，杜绝任意 .jsonl 读取。
10. ~~**memstat 泄漏检测误报不可调**~~ → 已修复：阈值与节奏改为可配置（见 6.10 配置表），高吞吐服务可调大阈值降低误报。
11. ~~**clientstat 停用残留**~~ → 已处理：`src/stat/clientstat/` 与其测试已删除，ip2region 依赖已从 go.mod 移除。如未来需要该功能，从 git 历史恢复。

### 仍然存在

5. **登录 bcrypt 有 10s 时间窗**：客户端与服务端时钟偏差 >10s 会登录失败；且 `Unix()/10` 窗口边界处可能偶发一次失败（重试即可）。改造认证时注意保留防重放语义。
6. **token 仅存内存**：tokenx Memory 存储，宿主应用重启后所有 OM 会话失效（需重新登录）。这与 restart 场景天然兼容，但做集群/多副本时不可共享。
7. **restart.sh 依赖 curl/pkill/nohup/nice**：目标机器必须有这些命令且 RunCommand 全部经 `nice` 包装；非 Linux（macOS 开发机）下 watchdog 脚本里的 top/free 语法不适用——**部署相关接口只能在 Linux 生产机上验证**。
12. **测试污染工作目录 / 存量测试缺陷**：test/ 下多数测试直接在工作目录读写 `.logs/.cache/.deploy`；其中 `TestListLogFiles`/`TestSearchLog*`/`TestMonitorLogs` 依赖仓库根目录的 `../../.logs` 夹具（当前缺失会失败）、`TestHostClear` 受后台 cron 与残留数据干扰属偶发——均为存量问题，与源码逻辑无关。跑完注意 `git status`，`.gitignore` 已忽略这些产物。

---

## 12. 快速上手（开发者视角）

```bash
# 构建
go build ./...

# 本地起一个带 OM 的最小宿主（需配置文件提供 om.key，端口默认 :9617）
cd simple && go run .

# 跑单个集成测试（示例；需先在 test/ 下准备 local.yaml 提供 om.key 等配置，
# 该文件被 .gitignore 忽略，需自行创建）
go test ./test/ -run TestApiStatWorkflow -v

# 登录拿 token（伪代码）
pwd_hash = bcrypt(unix_ts()/10 + om_key)
curl -X POST :9617/om/auth/connect -d '{"pwd":"$pwd_hash"}'
# 之后所有请求带 Authorization: Bearer <token>
```

> 前端面板地址：<https://jom-io.github.io/gorig-om> ，连接时填宿主地址与 `om.key`。
