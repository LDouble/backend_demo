# 交易与支付领域架构

## 领域边界

```text
Marketplace / Errand / Material / Paid Help
                    │ 创建可信交易
                    ▼
             Trade Order Domain
                    │ 在线支付时创建
                    ▼
              Payment Domain
                    │
                    ▼
          Domain Events → Notification
```

- 业务模块负责资源价格、资源所有权和详细履约状态。
- Trade 负责订单号、交易双方、金额快照、通用交易状态、参与方访问控制和状态审计。
- Payment 负责支付意图、渠道尝试、退款及回调幂等；当前 Marketplace 为线下付款，不创建 PaymentIntent。
- 客户端不能调用通用接口提交任意金额创建订单；订单必须由业务模块根据锁定后的资源生成。

## 当前模型

Trade 包含 `Order` 和 `OrderTransition`。订单通过 `order_type`、`resource_type`、`resource_id` 关联业务资源，不建立跨模块领域依赖；`buyer_id + idempotency_key` 唯一防止重复下单。

Marketplace 包含 `Listing`、`ListingImage` 和 `MarketplaceReservation`。Reservation 只记录商品保留、Trade Order 关联和 48 小时到期信息，不再保存金额、卖家或通用订单状态。

Errand 包含 `Task` 和 `Transition`。Task 是跑腿履约聚合，保存取送地点、截止时间、发布者、接单人及取件/送达/完成时间；Transition 保存每一次任务状态变化。其状态机为 `open → accepted → picked_up → delivered → completed`，其中 `open` 或 `accepted` 可由任务双方取消。接单使用任务行锁和乐观锁版本，在同一事务创建 `order_type=errand` 的线下 Trade Order；发布者是买方，跑腿员是卖方，报酬与双方均由锁定后的 Task 推导。

Payment 包含 `PaymentIntent`、`PaymentTransaction`、`PaymentRefund` 和 `PaymentCallback`。本阶段只建立持久化模型及内部 Provider 契约，不开放支付 HTTP API，也不配置渠道。

## Marketplace 事务

创建订单时在同一 MySQL 事务内：

1. 使用 `FOR UPDATE` 锁定 Listing；
2. 校验商品已发布且买家不是卖家；
3. 从 Listing 生成金额、卖家和标题快照；
4. 创建状态为 `confirmed`、支付方式为 `offline` 的 Trade Order；
5. 创建 MarketplaceReservation 并将 Listing 改为 `reserved`；
6. 写入 OrderTransition 和 `domain_events`；
7. 提交事务。

取消、完成、管理员下架和超时释放同样原子更新 Trade Order、Reservation、Listing、Transition 和领域事件。普通用户只能查询自己作为买家或卖家的订单。

## 扩展规则

资料和付费求助应像跑腿一样创建各自的履约聚合，再由业务服务创建 Trade Order。详细履约状态留在业务模块，Trade 只保存 `fulfillment_status` 摘要。打赏没有履约生命周期时直接关联 PaymentIntent，无需强制创建 Trade Order。

线上支付通过 `resource_type=trade_order` 关联订单。支付回调必须验签，并使用 `(provider, provider_event_id)` 唯一键去重；支付事务写入 `payment.succeeded` 等领域事件，由 Trade 订阅后更新订单，通知失败不得回滚支付事实。
