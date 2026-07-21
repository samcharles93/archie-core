# agentexec-protocol

我现在掌握了所有需要的信息。以下是完整的报告。

---

## 报告：Archie 核心中的代理执行协议和 Runner 架构

### 1. Runner 接口

定义位置：`/work/apps/archie-core/internal/agentexec/inprocess.go`，第 20-22 行

```go
type Runner interface {
    Run(ctx context.Context, workspace string, req Request) (Result, error)
}
```

**一个单一方法接口：** `Run` 接受一个上下文、一个工作目录路径（字符串）和一个 `Request`，返回一个 `Result` 和一个错误。工作目录路径与请求分开传递，供容器运行器将主机路径映射到内部文件系统。

该接口有两个具体实现：

- **`InProcessRunner`** (`/work/apps/archie-core/internal/agentexec/inprocess.go`，第 29-33 行) -- 拥有一个 `*runtime.Runtime`、一个 `*slog.Logger` 和一个 `loopFunc` 字段（默认为 `agentloop.Run`）。它在与 `archied` 相同的进程空间中运行代理循环。

- **`SubprocessRunner`** (`/work/apps/archie-core/internal/agentexec/subprocess.go`，第 17-27 行) -- 拥有 `Command`、`Args`、`Environ`、`AdditionalEnv`、`Diagnostics io.Writer` 和 `Providers`。它将完整的 `Invocation` 序列化到 stdin，启动一个 `archie-agent` 二进制文件，并从 stdout 解码 `Response`。

### 2. 线路协议

所有消息类型都定义在 `/work/apps/archie-core/internal/agentexec/protocol.go` 中。

**Invocation** (第 139-144 行) -- 被 `archie-agent` 接受的完整请求：
```go
type Invocation struct {
    Version   int                 `json:"version"`
    Workspace string              `json:"workspace"`
    Request   Request             `json:"request"`
    Providers map[string]Provider `json:"providers"`
}
```

**Request** (第 59-74 行) -- 代理阶段的有效载荷：
```go
type Request struct {
    Version      int           `json:"version"`
    TaskID       int64         `json:"task_id"`
    Attempt      int           `json:"attempt"`
    Stage        string        `json:"stage"`
    Model        string        `json:"model"`
    Mission      string        `json:"mission"`
    ExtraRules   string        `json:"extra_rules,omitempty"`
    ReadOnly     bool          `json:"read_only,omitempty"`
    Budget       Budget        `json:"budget"`
    Gate         Gate          `json:"gate"`
    Preflight    []Command     `json:"preflight,omitempty"`
    Protection   Protection    `json:"protection"`
    Notes        string        `json:"notes,omitempty"`
    CaptureTools []CaptureTool `json:"capture_tools,omitempty"`
}
```

**Result** (第 97-111 行) -- 输出：
```go
type Result struct {
    Version       int                          `json:"version"`
    TaskID        int64                        `json:"task_id"`
    Attempt       int                          `json:"attempt"`
    Stage         string                       `json:"stage"`
    Status        string                       `json:"status"`
    StopReason    string                       `json:"stop_reason,omitempty"`
    Changes       []string                     `json:"changes,omitempty"`
    Iterations    int                          `json:"iterations,omitempty"`
    TokensUsed    int                          `json:"tokens_used,omitempty"`
    Summary       string                       `json:"summary,omitempty"`
    Detail        string                       `json:"detail,omitempty"`
    AppendedNotes []string                     `json:"appended_notes,omitempty"`
    Captures      map[string][]json.RawMessage `json:"captures,omitempty"`
}
```

**Response** (第 168-172 行) -- 从 `archie-agent` 返回的顶层信封：
```go
type Response struct {
    Version int    `json:"version"`
    Result  Result `json:"result"`
    Error   string `json:"error,omitempty"`
}
```

**辅助类型：**
- `Command` (第 18-22 行)：`Name`、`Argv`、`ExpectFailure`
- `Gate` (第 25-28 行)：`Commands`、`MaxConsecutiveFailures`
- `Budget` (第 31-35 行)：`MaxSteps`、`MaxTokens`、`WallClock`
- `Protection` (第 38-41 行)：`Suffixes`、`Globs`
- `CaptureTool` (第 45-53 行)：`Name`、`Description`、`Parameters`、`RequiredFields`、`NonEmptyStrings`、`BooleanFields`、`MaxCalls`
- `Provider` (第 115-119 行)：`Class`、`APIKeyEnv`、`BaseURL`

协议通过 JSON 在 stdin/stdout 上使用。`ServeOne` (`worker.go`，第 16-43 行) 解码一个 `Invocation`，验证它，构建一个 `InProcessRunner`，运行它，然后将单个 `Response` 写入 stdout。`ServeOne` 不会循环——它处理一个调用然后返回。

### 3. cmd/archied 如何连接 Runner

在 `/work/apps/archie-core/cmd/archied/main.go` 中，第 109-125 行：

```go
providers := executionProviders(cfg)
llm := agentexec.NewRuntime(providers)
var agentRunner agentexec.Runner
if llm != nil {
    switch cfg.Agent.Mode {
    case "subprocess":
        agentRunner = &agentexec.SubprocessRunner{
            Command:       cfg.Agent.Command,
            Environ:       os.Environ(),
            AdditionalEnv: cfg.Agent.Env,
            Diagnostics:   os.Stderr,
            Providers:     providers,
        }
    case "inprocess":
        agentRunner = agentexec.NewInProcessRunner(llm, log)
    }
}
```

运行器模式由 `cfg.Agent.Mode` 控制，它来自配置 TOML。有两个 case：

- **`"subprocess"`**：创建一个 `SubprocessRunner`，将完整的环境、一个可执行文件命令（来自 `cfg.Agent.Command`，通常是 `archie-agent` 二进制文件的路径）以及提供者映射传递给它。
- **`"inprocess"`**：通过 `NewInProcessRunner(llm, log)` 创建一个 `InProcessRunner`，其中 `llm` 是 `runtime.NewRuntime(providers)` 的结果。

然后，`agentRunner` 被放入 `Daemon` 结构体 (`/work/apps/archie-core/internal/daemon/daemon.go`，第 33 行) 作为 `Agent agentexec.Runner`。守护进程的 `process` 方法 (第 356-377 行) 将其传递到 `TaskContext`：

```go
workflow.Run(ctx, wf, &workflow.TaskContext{
    Task:         task,
    Repo:         repo,
    ...
    Agent:        d.Agent,
    ...
})
```

如果 `cfg.Agent.Mode` 既不是 `"subprocess"` 也不是 `"inprocess"`，或者如果 `llm` 是 nil（没有提供者），则 `agentRunner` 保持为 nil。当 `Agent` 为 nil 时，`AgentStage.Stage()` 方法 (`agent.go`，第 49-50 行) 返回一个合适的错误。

### 4. TaskContext 和 Agent 字段

定义位置：`/work/apps/archie-core/internal/workflow/workflow.go`，第 26-57 行。

```go
type TaskContext struct {
    Task  *store.Task
    Repo  config.Repo
    Cfg   config.Config
    Forge forge.Forge
    Store *store.Store
    Trees *worktree.Manager
    Agent agentexec.Runner          // <-- 第 33 行
    Bus   *events.Bus
    Log   *slog.Logger
    CustomStages func(dir string) ([]Stage, error)
    Dir    string
    Branch string
    BuildSummary string
    ReproProof string
    decision *decision
    Outcome Outcome
}
```

`Agent` 字段是 `agentexec.Runner`。工作流通过 `AgentStage.Stage()` (`agent.go`，第 47-148 行) 调用它。核心调用顺序是：

1.  `AgentStage.Stage()` 从上下文字段 (`agent.go`，第 94-109 行) 构建一个 `agentexec.Request`。
2.  调用 `tc.Agent.Run(ctx, tc.Dir, req)` (第 110 行)。
3.  然后处理结果：验证 (`ValidateFor`，第 114 行)、追加笔记 (第 117-124 行)、累积代币/迭代次数 (第 129-130 行)、检查非 `"passed"` 状态 (第 136-141 行)，以及分派给 `OnResult` 回调 (第 143-145 行)。

只有 `InProcessRunner` 使用 `runtime.Runtime` 直接调用 `agentloop.Run`。`SubprocessRunner` 构建一个 `Invocation`，通过提供者名称映射将完整的请求序列化，并通过 `exec.CommandContext` 启动一个子进程。

### 5. 使 `archie-agent` 成为长期运行的消息总线进程所需的变化

以下是关键的变化领域：

**A. 入口点 (`cmd/archie-agent/main.go`)**

- 目前它调用 `agentexec.ServeOne`，后者解码一个 JSON 调用，执行，并将一个 JSON 响应写入 stdout。这严格是一次性的。
- 要变为长期运行，`main()` 需要初始化一个消息总线客户端（例如，NATS、RabbitMQ、AMQP 甚至是简单的 gRPC 流），订阅一个主题/队列，并在每个传入的 `Invocation` 上循环调用 `ServeOne`（或者更确切地说，其核心逻辑）。
- 信号处理需要优雅地等待进行中的工作完成，而不是在 `SIGTERM` 时直接退出。

**B. worker.go 中的 `ServeOne` 函数**

- 目前它在全局范围内构建一个 `InProcessRunner`（将 `NewRuntime(invocation.Providers)` 传递给 `NewInProcessRunner`）。
- 如果进程是长期运行的，则 `runtime.Runtime` 应该在启动时构造一次（可能来自配置或环境变量），而不是每个调用都动态构建。提供者配置会在进程的生命周期内保持不变，因此没有理由每次从 JSON 重新创建运行时。
- `protocol.go` 中的 `Invocation` 类型包含 `Providers`——如果一个长期运行的 worker 已经在启动时知道其提供者，这可能就不需要了。工作区路径也有问题：对于长期运行的进程，工作区可能由消息路由到（或使用单独的 RPC 调用来准备），而不是嵌入在调用请求中。

**C. 线路协议适配**

- 当前协议是 request/reply（1 个 JSON 值用于输入，1 个用于输出）。对于总线，`Response` 会被发布到单独的回复主题/队列，而不是写入 stdout。
- 运行器接口本身 (`Runner.Run(ctx, workspace, req)`) 是同步的，这很适合总线——每个消息都可以通过 `Run` 处理，结果被发布出去。
- 调用/响应帧中的 `Version`、`TaskID`、`Attempt`、`Stage` 字段提供了足够的路由/关联上下文——`Response` 中的 `TaskID` 和 `Stage` 使得在回复主题上正确路由变得简单。

**D. 环境架构**

- 目前，`SubprocessRunner` 通过继承的环境变量传递 API 密钥，并使用 `WorkerEnvironment` 进行过滤（`subprocess.go`，第 155-162 行）。一个长期运行的进程已经在进行中，但需要能够更新/重新加载提供者凭证而不重新启动（例如，密钥轮换）。这可能需要一个控制通道或一个动态重新加载配置的方法。

**E. 配置和工作目录**

- 目前，`Invocation` 承载 `Workspace`，并且每个路径都是请求的一部分。一个基于总线的 worker 可能希望要么：
  - 在工作区已经准备就绪时接收带外消息（例如，一个单独的“准备”RPC），或者
  - 除了运行代理之外，还执行一些较小的设置步骤（克隆 repo）。
- 守护进程当前通过 `worktree.Manager` (`steps.go`，第 22-30 行) 处理工作目录准备。如果 `archie-agent` 处理其自己的工作目录设置，这会创建一个职责划分问题。

**F. `SubprocessRunner` 对 `agentexec` 包的依赖**

- `SubprocessRunner` 是 `archied` 端的替代品——它从 `archied` 启动 `archie-agent`。如果 `archie-agent` 是一个长期运行的进程，`archied` 不会直接启动它；连接成为出站消息总线连接。`SubprocessRunner` 可能会变得过时或需要重构为一个“远程”运行器，该运行器通过总线发送消息而不是生成子进程。

**G. 错误处理和死信队列**

- 目前，`cmd.Run()` 中的进程退出代码表示协议失败。对于长期运行的进程，反序列化故障或无效调用应发布到死信队列主题。

总而言之，`Runner` 接口 (`Run(ctx, workspace, req)`) 本身不需要改变。然而，它的调用方式会改变：
- `cmd/archie-agent/main.go` 将从一次性 stdin/stdout 变为总线主题订阅循环。
- `cmd/archied/main.go` 将使用一个基于总线的远程运行器，而不是 `SubprocessRunner`。
- 提供者查找和运行时生命周期管理将从每次调用（`worker.go` 中的 `ServeOne`）移动到进程启动时。