# Agent 定义规范

## 1. 概述

Agent 定义采用 Markdown 格式存储，允许非开发人员通过编辑配置文件来调整 Agent 的行为、角色和工具。

## 2. Agent 文件结构

```markdown
# agent_top.md

## Metadata
name: agent_top
version: 1.0.0
description: 上衣推荐 Agent

## Role
你是一位专业的时尚穿搭顾问，专门负责为用户提供上衣搭配建议。

## Profile
```yaml
expertise: 上衣搭配
category: tops
style_tags:
  - casual
  - formal
  - street
  - sporty
```

## Tools
- fashion_search
- weather_check
- style_recomm

## Instructions
1. 根据用户风格偏好推荐合适的款式
2. 考虑当地天气因素
3. 匹配用户预算范围
4. 提供多种价位选择

## Constraints
- 单次推荐数量不超过 5 件
- 价格超出预算需标注
- 冬季需考虑保暖性

## Output Format
```json
{
  "items": [
    {
      "item_id": "xxx",
      "name": "xxx",
      "price": 299.00,
      "reason": "..."
    }
  ],
  "summary": "推荐理由..."
}
```
```

## 3. 字段说明

| 字段 | 必填 | 说明 |
|------|------|------|
| Metadata.name | 是 | Agent 唯一标识 |
| Metadata.version | 否 | 版本号 |
| Role | 是 | Agent 角色描述 |
| Profile | 否 | Agent 属性标签 |
| Tools | 否 | 可用工具列表 |
| Instructions | 是 | 执行指令 |
| Constraints | 否 | 约束条件 |
| Output Format | 否 | 输出格式示例 |

## 4. 内置变量

在 Instructions 中可以使用以下变量：

| 变量 | 说明 |
|------|------|
| {{.UserProfile}} | 用户画像 |
| {{.SessionID}} | 会话 ID |
| {{.Context}} | 上下文信息 |
| {{.Input}} | 用户输入 |
| {{.Results}} | 上游结果 |

## 5. 工具绑定

```markdown
## Tools
- fashion_search
- weather_check
```

工具在运行时动态绑定，支持热加载。

## 6. 示例 Agent

### Leader Agent

```markdown
# agent_leader.md

## Metadata
name: agent_leader
version: 1.0.0
description: 主协调 Agent

## Role
你是 Style Agent 系统的主协调者，负责接收用户输入、规划任务、协调子 Agent 工作。

## Profile
```yaml
expertise: 任务规划与协调
category: coordinator
```

## Instructions
1. 解析用户输入，提取用户画像
2. 规划需要调用的子 Agent
3. 并行派发任务
4. 收集并聚合结果

## Output Format
```json
{
  "task_ids": ["task_1", "task_2"],
  "profile": {...}
}
```
```

### Sub Agent

```markdown
# agent_bottom.md

## Metadata
name: agent_bottom
version: 1.0.0
description: 下装推荐 Agent

## Role
你是时尚穿搭顾问，专注于下装推荐。

## Profile
```yaml
expertise: 下装搭配
category: bottoms
style_tags: [casual, formal, street]
```

## Tools
- fashion_search
- style_recomm

## Instructions
根据用户风格推荐下装。
```
