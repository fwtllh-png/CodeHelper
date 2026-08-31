---
name: system-refactor
description: Executes cross-module refactors with explicit ownership and integration checks. Invoke for broad structural changes. 大型重构 跨模块重构
description_zh-CN: 通过明确所有权和集成检查实施跨模块重构；大范围结构调整时使用。
---
# 系统化重构

先确认现有边界、不变量、公开契约和基线测试，再把工作拆成可独立验证的切片。每个切片
完成后立即运行最窄验证，最后执行跨模块集成检查。

只有写入路径互不重叠、接口契约已经冻结且并行收益明确时，才委派多个 `implementer`
Subagent。为每个 Child 声明唯一 `owned_paths`、预期输出和验证命令。共享接口、
生成文件、迁移和最终整合由 Parent 串行处理。

不要为了并行而复制抽象、保留无需求的兼容层或扩大 Child 权限。
