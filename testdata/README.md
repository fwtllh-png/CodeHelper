# Test Data

该目录保存 Go 测试直接使用的最小输入数据（例如 `providers/` 下的 HTTP/SSE fixture）。

`benchmarks/` 是 hermetic coding benchmark 任务集，由 `internal/host/bench` 执行（`make bench`），格式见 [benchmarks/README.md](./benchmarks/README.md)。

协议场景与契约门禁文件已从本仓库移除；新增 fixture 时就近放在对应包的 `testdata/` 或本目录，并在测试中直接引用。
