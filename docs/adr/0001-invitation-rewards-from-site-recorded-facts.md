# 0001. Invitation Rewards From Site-Recorded Facts and a Seven-Day Observation Window

## Status

Accepted

## Context

Inviting users needs two numbers the site does not naturally have: what a recharge was *worth*, and *when* it can be considered final.

The only recharge events TokenMetro can observe are its own successful redemptions (`RechargeEvent`). The linked shop's order data is not visible to the site — `SaleOrderId` has never had a trustworthy source — so the price a buyer actually paid (after shop balance discounts, reseller markups, or agent pricing) cannot be used. The shop's after-sale policy does, however, allow a purchase to be refunded within seven days of sale, which means a recharge can still be reversed after the site has already granted the entitlement.

## Decision

1. **Reward basis is the site's own listed face value** of the redeemed code — the code's recorded quota for balance codes, and the referenced plan's listed price for subscription codes. Not the linked-shop sale price.
2. **Rewards are deferred.** A successful redemption creates a *pending* reward; only after a **seven-day observation window** does a periodic settlement job credit it into the inviter's reward balance (`aff_quota` / `aff_history`).
3. **The ratio is snapshotted at recharge time.** The ledger row stores both the ratio and the final `reward_quota`; settlement moves value, it does not recompute it.
4. **Within the window the recharge can be marked ineligible** by an administrator (refund case), which voids the pending reward. Rewards that have already settled are never clawed back.
5. **Only sold codes earn rewards.** A batch-level flag marks granted codes (promotional, compensation, trial, test); redeeming one delivers its entitlement but produces no reward. The default is "earns rewards", so a forgotten flag can only over-pay a small gift, never silently drop a real purchase's rebate.

## Consequences

- Reseller markups, agent discounts and shop-balance discounts do not change the reward; a reward may legitimately differ from what the buyer paid.
- The inviter sees a *pending* amount for seven days, so the UI must expose that state — otherwise settled-only totals look like missing money.
- A ledger table and a settlement job are required; the existing aggregate columns alone cannot express pending, settled, and voided states.
- If the invitee burns the quota or the account is blocked during the window, the reward still settles. Recovery is out of scope by explicit decision.
- Exceptional refunds after settlement are handled manually by adjusting the user's quota; the reward itself is not reversed.
