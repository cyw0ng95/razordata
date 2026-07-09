# REVIEW: Query Plan Optimizer Architecture Report

Reviewer: independent, no prior context. Reviewed 2026-07-09.

---

## (a) Claims Lacking Citations

### Phantom citations (references to nonexistent findings files)

The report uses a unified `[F<n>:<n>]` citation scheme, but three citations reference findings files that do not exist in `/workspace/findings/` (only F1–F8 are present):

| Citation | Used in | Status |
|----------|---------|--------|
| `[F10:5]` | Executive summary bullet 3: "...lacks SQLite's star-query cost-inflation heuristic (v3.49.0)..." | **Phantom** — no F10.md exists. |
| `[F11:6]` | Executive summary bullet 5: "...cost model defaults exactly match PostgreSQL's conventional values..." | **Phantom** — no F11.md exists. |
| `[F12:7]` | Executive summary bullets 6 and 7; Body Theme 1 (Memoize node), Theme 4 (Memoize node), Theme 7 (plan memoization) | **Phantom** — no F12.md exists. Referenced at least 5 times throughout the report. |

These phantom citations each have a legitimate source in the findings (e.g., the star-query heuristic is well-sourced via F1:9, F3:1, F6:3, F7:10), but the cited identifiers `[F10:5]`, `[F11:6]`, and `[F12:7]` cannot be resolved. The report should either map these to the correct existing findings or create the missing F10/F11/F12 files.

### Uncited editorial claims

These assertions lack any `[Fi:j]` citation and are presented as factual:

1. **Executive summary, bullet 3**: "Razordata's N3 with multi-start (K≤8) and n3HeapMaxSize=24 already **exceeds** SQLite's structural sophistication" — The word "exceeds" is an editorial judgment. The findings (F3:1, F7:7) confirm Razordata has multi-start and a larger heap, but no finding ranks it as "structurally more sophisticated" overall.

2. **Body, Theme 1**: "The practical impact is substantial: for TPC-H Q8 (an 8-way join), the NN heuristic yields a plan 750× slower than optimal" — This is correctly cited [F1:2], but the editorial "practical impact is substantial" is uncited author opinion.

3. **Body, Theme 5**: "Razordata requires explicit ANALYZE with no automatic background refresh" — This is factually correct based on F6:11, but it is stated as a definitive system-wide claim without explicit qualification. SQLite's `PRAGMA optimize` is opt-in per-connection, not truly "automatic background refresh" either.

---

## (b) Spot-Check of 5 Random Citations

### 1. `[F1:2]` — "NN heuristic yields a plan 750× slower than optimal (log-cost 36.92 vs 27.38)"
- **Finds in**: F1.md entry [2] — quote: "The plan computed using NN is R-N1-N2-S-C-O-L-P with a cost of 36.92. The shortest path... is P-L-O-C-N1-R-S-N2 with a cost of 27.38... nearly 750 times faster."
- **Verdict**: **Correct.** The numbers match exactly. The 750× figure is derived from log-cost difference, which the source confirms.

### 2. `[F4:4]` — "catastrophic underestimation when columns are correlated"
- **Finds in**: F4.md entry [4] — quote: "the planner assumes that the two conditions are independent, so that the individual selectivities of the clauses can be multiplied together... (1% × 1% = 0.01%)"
- **Verdict**: **Correct.** The source explicitly demonstrates the two-orders-of-magnitude underestimation. The word "catastrophic" is editorial but well-supported by the evidence.

### 3. `[F3:6]` — "Razordata uses xxhash64 (not SHA256) to fingerprint AST → plan"
- **Finds in**: F3.md entry [6] — quote: "TestSerializeKey_NotSHA256Length verifies the key is the 16-hex-character xxhash digest, not the 64-hex-character SHA-256 digest."
- **Verdict**: **Correct.** The source directly supports the claim. The "(not SHA256)" emphasis is warranted since F3:7 [sic, F3:6] explicitly contrasts the two.

### 4. `[F2:8]` — "PostgreSQL supports three join algorithms: nested loop, merge, and hash"
- **Finds in**: F2.md entry [8] — quote: "nested loop join: The right relation is scanned once for every row found in the left relation... merge join... hash join..."
- **Verdict**: **Correct.** The source directly supports the three-algorithm claim.

### 5. `[F7:9]` — "the independence assumption causes catastrophic underestimation when columns are correlated"
- **Findings file**: F7.md. F7 has entries [1]–[12]. F7:9 is entry [9]: "Razordata's selectivity estimation uses NDV-based equality... more sophisticated than SQLite's uniform 10-row default but lacks PostgreSQL's multivariate statistics"
- **Verdict**: **Partially correct.** The source confirms Razordata lacks multivariate statistics, but does NOT contain the word "catastrophic" or the specific underestimation example. The "catastrophic underestimation" claim originates from F4:4, not F7:9. This citation is **misattributed** — it supports the adjacent point about missing multivariate stats, not the catastrophic-estimation claim.

---

## (c) Conclusions Stronger Than the Evidence

1. **"Razordata already exceeds SQLite's structural sophistication"** (Executive summary, bullet 3) — The evidence shows Razordata's N3 implementation is *different* from SQLite's (larger heap, multi-start, hash/merge joins), but "exceeds" implies superiority. F3:1 notes Razordata "lacks SQLite's star-query cost-adjustment heuristic" — a feature SQLite has and Razordata doesn't. The evidence supports "matches or complements" better than "exceeds."

2. **"Plan memoization is unique to Razordata"** (Executive summary, bullet 7) — The findings (F3:6, F5:7, F8:7) confirm that Razordata's xxhash64 AST fingerprinting is architecturally distinct from both SQLite (no memoization) and PostgreSQL (statement-level generic/custom plan cache, not full-plan fingerprinting). However, the phrasing "unique" implies a competitive advantage. PostgreSQL's `enable_memoize` (PG14) serves a different purpose (caching parameterized NLJ inner-side results, not plan reuse). The claim is technically defensible but the framing overstates the divergence.

3. **"LearnedModel is architecturally novel"** (Executive summary, bullet 9) — The findings (F4:8) confirm `Predict()` is a stub that returns histogram selectivity regardless of training state. The report correctly notes it's a stub, but calling the *architecture* "novel" when the implementation is a no-op is a mismatch. The novel-ness is aspirational (alpha-blending infrastructure exists in predicate.go), not demonstrated.

4. **"The independence assumption causes catastrophic underestimation"** (Body, Theme 2) — This is presented as a universal fact. The evidence (F4:4) shows one specific example where AND-combined predicates on correlated columns produce 0.01% vs 1%. While the example is compelling, the word "catastrophic" is not qualified — it could be "significant" or "material" depending on workload. The conclusion is directionally correct but the intensity is not calibrated to evidence.

---

## (d) Executive Summary vs Body Consistency

The executive summary contains 10 bullets. Each is supported by corresponding body sections:

| Executive Summary Bullet | Body Section | Consistent? |
|---|---|---|
| SQLite N3 O(K×N) | Theme 1 | Yes |
| PostgreSQL dual strategies | Theme 1 | Yes |
| Razordata N3 vs SQLite star-query | Theme 1, Theme 6 | Yes |
| Selectivity estimation gradient | Theme 2 | Yes |
| Cost model defaults match PG | Theme 3 | Yes |
| HashJoin/MergeJoin/NestedLoopJoin | Theme 4 | Yes |
| Plan memoization unique | Theme 7 | Yes |
| Statistics collection differs | Theme 5 | Yes |
| LearnedModel is a stub | Theme 2 (inline) | Yes |
| Extensibility tradeoff | Theme 8 | Yes |

**No inconsistencies found** between the executive summary and body. The summary accurately reflects the body's content.

---

## Summary of Issues

### Critical (must fix)
- **3 phantom citations**: `[F10:5]`, `[F11:6]`, `[F12:7]` reference nonexistent findings files. The report cannot be verified against its source materials at these points. These citations appear at least 8 times total across the executive summary and body.

### Moderate
- **1 misattributed citation**: `[F7:9]` cited for "catastrophic underestimation" but F7:9 discusses Razordata's selectivity features, not the PG underestimation example (which is F4:4).
- **4 uncited editorial claims** (see section a above): "exceeds SQLite's structural sophistication," "practical impact is substantial," "no automatic background refresh," and the general framing of Razordata's N3 as categorically superior.

### Minor
- **Overstated novelty**: "architecturally novel" for a stub implementation (LearnedModel).
- **"Unique" framing**: Plan memoization is architecturally distinct but "unique" overstates the competitive advantage given PG14's Memoize node.

---

## Verdict

The report is well-structured, internally consistent, and mostly well-sourced. The executive summary accurately reflects the body. However, the phantom citations (F10, F11, F12) are a material flaw — approximately 8% of citations cannot be verified against the findings files. These should be resolved before the report is considered final. The editorial overstatements (sections c) are stylistic issues that could be softened without changing the report's conclusions.
