# 阶段 3：二手市场交易闭环与 Staging 上线模板

**Priority:** High  
**Status:** Done  
**Type:** Feature  
**Created:** 2026-07-22  
**Last Updated:** 2026-07-22

## Problem

现有二手市场已具备商品状态和线下订单事务，但成员缺少浏览、详情、编辑与个人商品查询入口，交易领域事件也未形成用户可见的站内通知；同时项目缺少满足 production 安全约束的通用 staging 交付模板。

## Goal

交付 Marketplace → Trade → Notice 的可验证线下交易闭环，并提供不包含真实环境凭据的单机 Docker Compose staging 模板。

## Scope

- 登录成员浏览已发布商品、查看本人商品和授权详情、编辑草稿/驳回商品。
- 管理员查询、审核和下架商品；下架与有效订单取消及预留释放保持原子性。
- 维持 CNY 线下交易：48 小时预留、双方取消、卖家完成、系统过期释放。
- 将商品审核/下架与订单创建/取消/完成/过期事件转换为幂等站内通知。
- Schema operation 声明默认内置角色并生成权限清单。
- 提供 Caddy、API、Worker、迁移、Bootstrap、MySQL、Redis TLS 的 staging Compose 与部署、备份、恢复、回滚手册。

## Out of Scope

- 在线支付、Payment HTTP API、真实微信通知和外部 push Provider。
- 真实主机、域名、证书、生产或 staging Secret。
- 自动连接或修改任何外部 staging 环境。

## Acceptance Criteria (EARS)

- WHEN 成员查询商品市场, THE SYSTEM SHALL 仅返回 `published` 商品，并按关键词、价格区间和分页筛选。
- WHEN 成员查询本人商品, THE SYSTEM SHALL 返回该成员所有状态商品，并支持状态筛选。
- WHEN 成员查询商品详情, THE SYSTEM SHALL 仅允许已发布商品、商品所有者或有效订单买家访问。
- WHEN 商品所有者修改 `draft` 或 `rejected` 商品并提供版本与幂等键, THE SYSTEM SHALL 原子更新内容、图片、联系方式和版本；IF 版本冲突, THE SYSTEM SHALL 返回冲突错误。
- WHEN 普通成员查看商品, THE SYSTEM SHALL 返回脱敏联系方式；WHEN 查看者是所有者或有效订单买家, THE SYSTEM SHALL 返回明文；WHEN交易终止或商品关闭, THE SYSTEM SHALL 再次脱敏。
- WHEN 管理员下架存在有效订单的商品, THE SYSTEM SHALL 在同一事务取消订单、释放预留并关闭商品。
- WHEN 两个买家并发购买同一商品, THE SYSTEM SHALL 仅允许一个订单成功并预留 48 小时。
- WHEN 买卖任一方取消订单, THE SYSTEM SHALL 释放商品；WHEN 卖家完成订单, THE SYSTEM SHALL 标记商品已售；WHEN 预留过期, THE SYSTEM SHALL 自动释放商品。
- WHEN `listing.reviewed`、`listing.removed`、`order.created`、`order.cancelled`、`order.completed` 或 `order.expired` 被投递, THE SYSTEM SHALL 为相应商品所有者或交易双方创建已发布、`push_enabled=false` 的站内通知和资源跳转。
- WHEN 同一领域事件至少一次重试, THE SYSTEM SHALL 通过唯一 `source_event_id` 避免重复通知；IF 通知或审计发布失败, THE SYSTEM SHALL 保留事件供现有重试流程处理。
- WHERE operation 声明 `default_roles: [member]`, THE SYSTEM SHALL 从 Schema 生成成员内置策略；WHERE 管理 operation 未声明默认角色, THE SYSTEM SHALL 不授予成员。
- WHERE staging 模板运行, THE SYSTEM SHALL 仅暴露 80/443，并在私网运行 API、Worker、MySQL 和 Redis；THE SYSTEM SHALL 在 production 模式强制 Redis TLS、服务端验证和客户端证书。
- WHEN 部署 staging, THE SYSTEM SHALL 先校验 Compose 并备份数据库，且仅在迁移成功后启动 API/Worker；WHEN 回滚应用, THE SYSTEM SHALL 使用上一镜像摘要；IF 需要数据库降级, THE SYSTEM SHALL 要求兼容性确认与新备份后显式执行。

## Technical Notes

- 版本化 SQL 迁移是 `source_event_id` 的事实来源；字段可空且唯一以兼容手工通知。
- 联系方式只在授权后的响应映射阶段解密，不进入领域事件、通知或审计日志。
- 通知订阅器先于结构化审计发布器执行，两者都成功后领域事件才标记 dispatched。
- staging 凭据来自受控环境文件，Redis 配置与证书来自只读挂载 Secret。
