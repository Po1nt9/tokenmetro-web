# TokenMetro Wallet & Redemption Context

TokenMetro 钱包负责展示账户可用额度、订阅权益、兑换码购买与兑换，以及邀请奖励。兑换码继续由现有链动小铺流程售卖；钱包只负责校验兑换并将兑换结果落到现有账户权益模型。

## Language

### Account Entitlements

**Wallet Balance**:
The account's spendable quota held in the existing wallet balance. It is separate from subscription entitlement and is consumed according to the gateway's billing preference.
_Avoid_: Subscription balance, points, cash balance

**Subscription Entitlement**:
A new-api subscription instance with its own plan, quota pool, validity period, reset schedule, and group effects. It is not converted into wallet balance.
_Avoid_: Subscription balance, package balance

**Redemption Code**:
The existing single-use code sold through the linked shop or granted by the operator, and redeemed by the authenticated account to receive the entitlement associated with that code's existing batch.
_Avoid_: Invitation code, coupon, gift card

**Granted Code**:
A redemption code the operator grants rather than sells — promotional, compensation, trial, or test batches. Redeeming one still delivers its entitlement, but it is not a recharge event and never earns an invitation reward.
_Avoid_: Gift card, coupon, free credit

**Redemption Outcome**:
The entitlement granted when a redemption succeeds: either wallet balance or a subscription entitlement. The user does not choose the outcome at redemption time.
_Avoid_: Code type, payment method

**Recharge Event**:
A successful redemption of a sold code that grants either wallet balance or a subscription entitlement. It is the source event used to calculate invitation rewards.
_Avoid_: Payment only, wallet top-up only

### Invitation Rewards

**Inviter**:
The existing account whose invitation relationship is attributed to a later account.
_Avoid_: Referrer, promoter

**Invitee**:
The account attributed to an inviter and whose successful recharge event may generate a reward.
_Avoid_: Referred user, subordinate user

**Invitation Reward Balance**:
A separately tracked reward balance credited to the inviter when an invitee's recharge event settles. It is not automatically merged into spendable wallet balance.
_Avoid_: Commission quota, wallet balance

**Reward Basis**:
The face value at which TokenMetro lists the redeemed code for sale, used to calculate the invitation reward for both wallet-balance and subscription redemptions. It is the value the site records for the code, not the price a buyer paid a reseller.
_Avoid_: Subscription quota, internal quota value, resale price

**Observation Window**:
The seven days after a recharge event during which the linked-shop purchase can still be refunded. No invitation reward is credited until the window has closed.
_Avoid_: Cooling-off period, settlement delay, hold

**Pending Invitation Reward**:
A reward earned by a recharge event whose observation window has not yet closed. It is visible to the inviter but is not yet part of the Invitation Reward Balance.
_Avoid_: Unconfirmed reward, bonus, provisional reward

**Reward Settlement**:
The step that credits a pending invitation reward into the Invitation Reward Balance once its observation window has closed. It is separate from transferring the reward balance into wallet balance.
_Avoid_: Payout, withdrawal, transfer

## Relationships

- A **Redemption Code** produces one **Redemption Outcome** at most once.
- A successful **Redemption Outcome** of a code that is not a **Granted Code** is a **Recharge Event**.
- A **Recharge Event** by an **Invitee** may create one idempotent **Pending Invitation Reward** for the **Inviter**.
- When its **Observation Window** closes, a **Pending Invitation Reward** is settled into the **Invitation Reward Balance**.
- **Reward Settlement** into the reward balance and transferring the reward balance into **Wallet Balance** are separate steps.
- **Wallet Balance** and **Subscription Entitlement** remain separate account entitlements.
