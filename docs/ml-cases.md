# Worked cases: classification, regression, forecasting, fraud and neural networks

Six business problems taken from data to a model in use, on realistic, messy
data: missing values, ID columns, dates, lopsided answers, outliers, and
columns that give the answer away. Each case says which columns to keep and
which to drop and why, which model and algorithm to pick, what Hermod
reported when it was trained (real runs, not estimates), how to read it, and
how to call the model.

Recommendations (visits, products, reorder, audience, similar) have their own
guide: [ml-recommendations-data-guide.md](ml-recommendations-data-guide.md)
and "Recommendation scenarios" in [ml.md](ml.md#recommendation-scenarios).

| Case | Question | Kind of model | Algorithm | Engine |
|---|---|---|---|---|
| [Late payment](#1-late-payment-risk-yesno-lopsided) | Will this invoice be paid late? | Predict a category (yes/no), 15% yes | `gradient_boosting` | built-in |
| [Ticket routing](#2-ticket-routing-six-teams) | Which team should take this ticket? | Predict a category, six classes | `linear` | built-in |
| [Delivery time](#3-delivery-time-a-skewed-number-with-outliers) | How many hours until delivery? | Predict a number | `gradient_boosting` | built-in |
| [Weekly demand](#4-weekly-demand-forecast-per-product-and-warehouse) | How many units next week, per product and warehouse? | Predict a number, as a forecast | `gradient_boosting` | built-in |
| [Card fraud](#5-card-fraud-one-in-a-hundred) | Is this payment fraud? | Predict a category (yes/no), 1% yes | `gradient_boosting`; `hist_gradient_boosting` or `xgboost` on the worker | either |
| [Account spend](#6-account-spend-a-neural-network-on-80000-rows) | What will this account spend next quarter? | Predict a number, with a neural network | `mlp`; `keras_mlp` or `pytorch_mlp` on the worker | either |

## What kinds of model Hermod trains

Hermod trains three kinds of model, and each case below is one of the first
two:

- **Predict a category** (`task: "classification"`): yes/no, or one of up to
  about 20 answers. Every version reports `accuracy`, `balanced_accuracy`,
  `f1` and, for yes/no, `roc_auc`.
- **Predict a number** (`task: "regression"`): reports `mae`, `rmse` and `r2`.
- **Recommend** (`task: "recommendation"` and the scenarios): see the
  recommendation guide.

There is no separate forecasting, anomaly-detection or clustering model.
What they are used for is done this way instead:

- **A forecast** is a number to predict, from a table built for it: one row
  per series and period, holding what is known at the end of the period
  (recent values, what is planned for the next one), with the next period's
  value as the target. [Weekly demand](#4-weekly-demand-forecast-per-product-and-warehouse)
  is the worked example, and shows how to judge it fairly.
- **Fraud and other anomalies with a known outcome** (chargebacks, confirmed
  cases) are a yes/no to predict: [Card fraud](#5-card-fraud-one-in-a-hundred).
  Without labels, the [Anomaly Score](ml.md#anomaly-score) workflow node
  scores how far a value stands out from its recent history per key; its
  score also makes a good column for a model, as in the fraud case.
- **A neural network** is an algorithm, not a kind: `mlp` on the built-in
  engine, `pytorch_mlp` and `keras_mlp` on the worker's `-dl` image, for
  either kind of prediction ([Account spend](#6-account-spend-a-neural-network-on-80000-rows)).

## The data

Each case has a generator in [`ml/examples/cases/`](../ml/examples/cases):
a Python script (numpy and pandas) that writes the case's table as a CSV
file. A fixed seed makes it write the same file every time, so every number
below can be reproduced. The data is synthetic, built to behave like the real
thing: the answer depends on several columns at once, with noise, and the
files carry the usual problems on purpose.

```bash
python3 ml/examples/cases/late_payment.py --out late_payment.csv          # full size, 40,000 rows
python3 ml/examples/cases/late_payment.py --rows 5000 --out small.csv     # any size
```

A sample of about 1,300 rows of each case but card fraud is kept in
[`ml/examples/cases/samples/`](../ml/examples/cases/samples) (gzipped CSV;
`gunzip` it before uploading). The numbers below are from the full-size
files, which are not kept in the repository.

To follow a case in the console: **Machine Learning → Data → Upload** the
CSV file as a dataset, then **Add model → Train a new model from my data**,
pick the target, untick the columns the case says to drop, and under
**Advanced** pick the algorithm. The same through the API is shown with each
case.

## How to read a result

Every version is scored on a fifth of the rows held back from training, and
reports the same metrics for a **baseline** that knows nothing: the most
common answer for a category, the average for a number. A model is worth only
its margin over the baseline.

- With a lopsided answer, **accuracy lies**: answering "no" every time is
  85% accurate on late payment and 99% on fraud. Read `balanced_accuracy`
  (the average of each answer's hit rate; 0.5 is guessing for yes/no, 1/6 for
  six classes) and `roc_auc` (how well the probability ranks the real yeses
  above the noes; 0.5 is guessing, 1 is perfect).
- For a number, read `mae` (the average miss, in the target's units) against
  the baseline's, and `r2` (the share of the variation explained; 0 is the
  baseline). A few huge outliers keep `r2` and `rmse` low even when most
  predictions are close; `mae` says more.
- `importance` is how much the score drops when a column is shuffled, as a
  share of all columns. A column you expected near the top that is not there
  is worth a look; a column that takes almost all of it may be a leak.
- A score that looks too good, such as a balanced accuracy near 1.0, almost
  always means a column gives the answer away.

## Columns to drop, in every case

The **pre-flight check** (the Train dialog's checklist, or
`POST /api/vhosts/{vhost}/ml/train-check`) gives every column a role. In
these cases it found:

| Role it gives | Columns in these cases | What to do |
|---|---|---|
| `id_like` | `invoice_id`, `ticket_id`, `shipment_id`, `order_id`, `tx_id`, `account_id`, and time stamps with seconds (`created_at`, `placed_at`, `delivered_at`) | Drop. |
| `leak_suspect` | `reminders_sent`, `resolution_code`, `handled_by`, `delivery_days`, `chargeback_filed`, `invoiced_next_quarter` | Drop: each is known only after the answer. |
| `mostly_empty` | `customer_satisfaction`, `nps`, `new_device`, `cvv_failed` | Depends: see the case. |

The check does **not** catch everything. It marks a column `use` when it
cannot prove a problem, and these columns were all marked `use` and must
still be dropped:

- **Leaks it cannot see:** `days_to_pay` and `paid_date` (late payment),
  `scan_events` (delivery time) and `revenue_next_week` (weekly demand). Each
  is known only afterwards, but none predicts the answer on its own well
  enough to be flagged. A late-payment model on five honest columns scores a
  balanced accuracy of 0.705; add `days_to_pay` and it scores **1.000**: a
  model that is perfect in training and useless on a new invoice, which has
  not been paid yet.
- **Dates as text:** `issue_date`, `due_date`, `quarter_end`, `week_start`.
  A date is read as text, so the model learns each day as its own category,
  which no future row will have. Turn a date into numbers that repeat (month,
  weekday, days since something) in the query or the export, as the cases do
  with `issue_month`, `placed_weekday` and `customer_tenure_days`.
- **High-cardinality IDs that are not unique per row:** `customer_id` (about
  3,000 values over 40,000 invoices), `card_id`, `merchant_id`, `sales_rep`,
  `product_id`, `series_id`. They are not ID-like by the check's rule (a value
  on 95% of rows), but only the 100 most common values of a text column are
  encoded, so what such a column can teach is little and does not carry over
  to new customers. Put what you know about the customer into columns of its
  own instead, such as `prior_late_share`.
- **Free text:** a ticket's `subject`. Take what matters out of it as yes/no
  columns (`kw_refund`, `kw_tracking`) and drop the text.

## 1. Late payment risk (yes/no, lopsided)

*Which invoices will be paid after their due date, so collections can call
before, not after?*

**Data.** [`late_payment.py`](../ml/examples/cases/late_payment.py): 40,000
invoices to about 3,000 business customers over two years, one row per
invoice. 14.7% were paid late. Whether one is depends mostly on the
customer's own habit, then on how stretched their credit is, disputes, the
payment terms and segment, and a little on the amount and month. New
customers have no history: 11% of rows have empty history columns.

| Keep | Drop |
|---|---|
| `issue_month`, `amount`, `currency`, `payment_terms_days`, `segment`, `industry`, `region` (3% empty), `customer_tenure_days`, `prior_invoices`, `prior_late_share` and `avg_days_late_prior` (empty for new customers), `credit_utilisation` (12% empty), `has_dispute` | `invoice_id` (ID), `customer_id`, `sales_rep` (high-cardinality), `issue_date`, `due_date` (dates as text), `reminders_sent` (dunning letters go out only once overdue; flagged), `days_to_pay`, `paid_date` (known once paid; not flagged) |

Leave the empty history values empty: Hermod fills a missing number with the
training median, and `prior_invoices` = 0 tells the model the row is a new
customer.

**Training.** Target `paid_late`, the 13 columns above, algorithm
`gradient_boosting`, on the built-in engine. Hermod sees that `yes` is only
15% of the rows and weighs the classes equally, so the model does not just
answer "no".

```bash
curl -X POST https://hermod.example.com/api/vhosts/default/ml/models/late_payment/train \
  -H 'Content-Type: application/json' -b cookies.txt -H "X-CSRF-Token: $CSRF" \
  -d '{"dataset": "late_payment", "target": "paid_late", "algorithm": "gradient_boosting",
       "features": ["issue_month", "amount", "currency", "payment_terms_days", "segment", "industry", "region",
                    "customer_tenure_days", "prior_invoices", "prior_late_share", "avg_days_late_prior",
                    "credit_utilisation", "has_dispute"],
       "go_live": {"mode": "always"}}'
```

**Result** (32,000 rows to train, 8,000 to test):

| | accuracy | balanced accuracy | F1 | ROC AUC |
|---|---|---|---|---|
| Model (`gradient_boosting`) | 0.708 | **0.709** | 0.611 | **0.790** |
| Baseline (always "no") | 0.853 | 0.500 | 0.460 | 0.500 |

The other built-in algorithms on the same split: `linear` 0.704 balanced
accuracy and 0.784 ROC AUC; `random_forest` 0.636 and 0.783. **Auto** with
the `balanced` preset compared them by 3-fold cross-validation on the
training rows (gradient boosting 0.711, linear 0.706, random forest 0.615)
and chose gradient boosting. On the worker, Auto with the `quick` preset
chose `hist_gradient_boosting`: balanced accuracy 0.717, ROC AUC 0.790.

**Reading it.** The model's accuracy (0.708) is *below* the baseline's
(0.853), and that is right: the baseline never finds a late invoice. The
model finds about seven in ten of them (its balanced accuracy is 0.709), and
its probability ranks a random late invoice above a random on-time one 79%
of the time (ROC AUC 0.790). Importance: `prior_late_share` 0.61,
`credit_utilisation` 0.15, `has_dispute` 0.08, `segment` 0.06; the rest
under 0.04. A customer's history is what matters, so keep it fresh in the
export.

**Using it.** Sort open invoices by `probability` of `yes` and call the top
of the list, rather than acting on the label. Three invoices through REST
(with a serving key made under **Serving key for apps**):

```bash
curl -X POST https://hermod.example.com/api/ml/serve/default/late_payment \
  -H "X-API-Key: hml_…" -H 'Content-Type: application/json' -d '{"instances": [
  {"issue_month": 12, "amount": 18400, "currency": "EUR", "payment_terms_days": 60, "segment": "public_sector",
   "industry": "construction", "region": "north", "customer_tenure_days": 410, "prior_invoices": 19,
   "prior_late_share": 0.42, "avg_days_late_prior": 16.5, "credit_utilisation": 0.97, "has_dispute": true},
  {"issue_month": 4, "amount": 950, "currency": "EUR", "payment_terms_days": 30, "segment": "enterprise",
   "industry": "healthcare", "region": "south", "customer_tenure_days": 2900, "prior_invoices": 140,
   "prior_late_share": 0.01, "avg_days_late_prior": 2.0, "credit_utilisation": 0.31, "has_dispute": false},
  {"issue_month": 6, "amount": 2300, "currency": "EUR", "payment_terms_days": 30, "segment": "smb",
   "industry": "retail", "region": "west", "customer_tenure_days": 12, "prior_invoices": 0,
   "prior_late_share": null, "avg_days_late_prior": null, "credit_utilisation": null, "has_dispute": false}]}'
```

```json
{"model":"late_payment","predictions":[{"label":"yes","probability":0.9522568980237125},{"label":"no","probability":0.9283293647902475},{"label":"yes","probability":0.580404798884965}]}
```

The third invoice is a new customer with nothing known: a `yes` at 0.58, a
coin toss leaning late, which is what new customers do in this data.

Over gRPC (`hermod.ml.v1.InferenceService/Predict`, key in the `x-api-key`
metadata), the first invoice answers:

```json
{"model": "late_payment", "predictions": [{"label": "yes", "probability": 0.9522568980237125}]}
```

Exposed to MCP clients (**AI assistants (MCP)** on), the model is the tool
`predict_late_payment`, with one argument per column. Over MCP a yes/no
column is typed as a number: send `"has_dispute": 1`, not `true` (`true` is
refused with *feature "has_dispute" must be a number*). The answer:

```json
{"model": "late_payment", "vhost": "default", "prediction": {"label": "yes", "probability": 0.9522568980237125}}
```

In a workflow, a **Predict** node on the invoices stream
(`{"model": "late_payment", "inputs": {}, "outputField": "late_risk"}`; empty
`inputs` sends the whole record, so the record must carry the 13 columns)
writes `{"label", "probability"}` to `late_risk`, and a Router can send
invoices above a probability to the collections queue.

## 2. Ticket routing (six teams)

*Which team should get a support ticket the moment it arrives?*

**Data.** [`ticket_routing.py`](../ml/examples/cases/ticket_routing.py):
30,000 tickets of an online retailer over a year, with the team that solved
each: billing 29%, shipping 26%, returns 18%, technical 14%, account 9%,
sales 4%. 27% quote no order, so their order columns are empty; 18% of
subjects hold no useful word.

| Keep | Drop |
|---|---|
| `hour`, `weekend`, `channel`, `customer_tier` (4% empty), `product_line`, `has_order_ref`, `order_value` and `days_since_order` (empty without an order), `attachments`, `previous_tickets_90d`, the eight `kw_*` keyword columns | `ticket_id` and `created_at` (ID-like), `subject` (free text: the keywords are taken from it), `customer_satisfaction` (70% empty, and asked after the ticket is solved), `resolution_code` and `handled_by` (set by the team that solved it: both flagged as leaks) |

The keyword columns are what a query can take from a subject:

```sql
SELECT ..., subject ILIKE '%refund%' AS kw_refund, subject ILIKE '%tracking%' AS kw_tracking, ...
FROM tickets
```

**Training.** Target `team`, the 18 columns above, algorithm `linear`.

**Result** (24,000 to train, 6,000 to test):

| | accuracy | balanced accuracy | F1 (macro) |
|---|---|---|---|
| Model (`linear`) | 0.713 | **0.733** | 0.686 |
| `gradient_boosting` | 0.706 | 0.726 | 0.681 |
| `random_forest` | 0.717 | 0.719 | 0.692 |
| Baseline (always billing) | 0.289 | 0.167 | 0.075 |

The three are within a point of each other; `linear` is the best balanced
accuracy and trained in about 3 seconds against 21 and 31 for the trees.
When yes/no columns add up to the answer, a linear model is enough.

**Reading it.** Seven tickets in ten go to the right team straight away,
against three in ten for "send everything to billing", and the small teams
are found as well as the big ones (balanced accuracy 0.733 against 1 in 6).
Importance: `has_order_ref` 0.18, `product_line` 0.12, `days_since_order`
0.12, `attachments` 0.07, then the keywords. The rest are wrong because the
ticket says too little; use the `probability` to route only the confident
ones and leave the others to a person.

**Using it.**

```bash
curl -X POST https://hermod.example.com/api/ml/serve/default/ticket_routing \
  -H "X-API-Key: hml_…" -H 'Content-Type: application/json' -d '{"instances": [
  {"hour": 21, "weekend": false, "channel": "chat", "customer_tier": "standard", "product_line": "furniture",
   "has_order_ref": true, "order_value": 389.0, "days_since_order": 3, "attachments": 0, "previous_tickets_90d": 1,
   "kw_invoice": false, "kw_refund": false, "kw_tracking": true, "kw_broken": false, "kw_password": false,
   "kw_price": false, "kw_return": false, "kw_charged_twice": false},
  {"hour": 10, "weekend": false, "channel": "email", "customer_tier": "business", "product_line": "laptops",
   "has_order_ref": false, "order_value": null, "days_since_order": null, "attachments": 0, "previous_tickets_90d": 3,
   "kw_invoice": false, "kw_refund": false, "kw_tracking": false, "kw_broken": false, "kw_password": false,
   "kw_price": true, "kw_return": false, "kw_charged_twice": false}]}'
```

```json
{"model":"ticket_routing","predictions":[{"label":"shipping","probability":0.9968804352898363},{"label":"sales","probability":0.9968159630501812}]}
```

In a workflow: the ticketing system's webhook or CDC source, a step that
sets the `kw_*` fields, a **Predict** node writing `team_guess`, and a
Router on `team_guess.probability` that assigns the confident tickets and
queues the rest.

## 3. Delivery time (a skewed number with outliers)

*How many hours will this parcel take, so the checkout can promise a day
and the support team can spot a late one?*

**Data.** [`delivery_time.py`](../ml/examples/cases/delivery_time.py):
40,000 parcels from six warehouses with five carriers over a year. Half
arrive within 66 hours, but the hours run long to the right (remote areas,
customs, heavy freight): the average is 79, and about 1 parcel in 70 goes
astray for a week or more for no reason any column shows. 7% of rows have no
recent lane average.

| Keep | Drop |
|---|---|
| `placed_hour`, `placed_weekday`, `peak_season`, `warehouse`, `carrier`, `service_level`, `destination_region`, `remote_area`, `distance_km`, `weight_kg`, `parcels`, `cash_on_delivery`, `cross_border`, `lane_avg_hours_30d` (7% empty) | `shipment_id`, `order_id`, `placed_at`, `delivered_at` (ID-like), `delivery_days` (the target in days: flagged), `scan_events` (tracking scans pile up the longer a parcel travels: correlates 0.9 with the target, not flagged) |

**Training.** Target `delivery_hours`, the 14 columns, `gradient_boosting`.

**Result** (32,000 to train, 8,000 to test):

| | MAE (hours) | RMSE | R² |
|---|---|---|---|
| Model (`gradient_boosting`) | **18.8** | 42.8 | 0.461 |
| `random_forest` | 19.8 | 43.8 | 0.434 |
| `linear` | 21.7 | 44.3 | 0.423 |
| Baseline (the average) | 38.7 | 58.3 | 0.000 |

**Reading it.** An R² of 0.46 looks weak, and is not: the parcels that went
astray cannot be predicted from anything known at checkout, and their
hundreds of hours dominate R² and RMSE. The average miss halves, from 38.7
hours to 18.8. Importance: `service_level` 0.27, `distance_km` 0.22,
`lane_avg_hours_30d` 0.20, `cross_border` 0.09, `remote_area` 0.07,
`peak_season` 0.07. To promise a date, add a margin to the prediction rather
than using it as it is, and treat a parcel far past its prediction as one to
chase.

**Using it.**

```bash
curl -X POST https://hermod.example.com/api/ml/serve/default/delivery_time \
  -H "X-API-Key: hml_…" -H 'Content-Type: application/json' -d '{"instances": [
  {"placed_hour": 10, "placed_weekday": 1, "peak_season": false, "warehouse": "WH-JKT", "carrier": "kilat",
   "service_level": "express", "destination_region": "region_02", "remote_area": false, "distance_km": 140.0,
   "weight_kg": 1.2, "parcels": 1, "cash_on_delivery": false, "cross_border": false, "lane_avg_hours_30d": 12.5},
  {"placed_hour": 18, "placed_weekday": 5, "peak_season": true, "warehouse": "WH-MKS", "carrier": "cargo_one",
   "service_level": "economy", "destination_region": "region_17", "remote_area": true, "distance_km": 2300.0,
   "weight_kg": 42.0, "parcels": 3, "cash_on_delivery": true, "cross_border": false, "lane_avg_hours_30d": null}]}'
```

```json
{"model":"delivery_time","predictions":[{"value":10.216206855905293},{"value":296.76876110182474}]}
```

A regression answers `{"value"}`; a Predict node writes the number itself to
its output field.

## 4. Weekly demand forecast per product and warehouse

*How many units of each product will each warehouse sell next week, to
order stock?*

**Data.** [`weekly_demand.py`](../ml/examples/cases/weekly_demand.py): 100
products in 10 categories, in five warehouses, over two years: 481 series
and 50,024 rows. The row for week W holds what is known at the end of week W
and the target is week W+1's units:

- the recent past: `units_last_week`, `units_2_weeks_ago`,
  `avg_units_last_4_weeks`, `avg_units_last_13_weeks`,
  `same_week_last_year` (empty while a series has less than a year of
  history: 37% of rows), `stockout_last_week` (that week's sales were cut
  short, so the lags understate demand);
- what is planned for next week: `price_next_week`, `promo_next_week`,
  `discount_next_week`, `holiday_next_week`, `week_of_year_next`;
- the series: `warehouse`, `category`.

Sales follow each series' level, a yearly season per category, a slow trend,
promotions (a third to double), price changes and holidays, with noise.

| Keep | Drop |
|---|---|
| the 13 columns above | `week_start` (a date as text; the week of the year is its own column), `series_id` and `product_id` (high-cardinality: the lags already carry each series' level), `revenue_next_week` (next week's units times price: known only after the week, and not flagged) |

The table comes from a weekly sales table with window functions, along these
lines (PostgreSQL):

```sql
SELECT week_start, warehouse, category,
       LAG(units, 1) OVER w AS units_2_weeks_ago, units AS units_last_week,
       AVG(units) OVER (w ROWS BETWEEN 3 PRECEDING AND CURRENT ROW) AS avg_units_last_4_weeks,
       AVG(units) OVER (w ROWS BETWEEN 12 PRECEDING AND CURRENT ROW) AS avg_units_last_13_weeks,
       LAG(units, 51) OVER w AS same_week_last_year,
       LEAD(price, 1) OVER w AS price_next_week, LEAD(on_promo, 1) OVER w AS promo_next_week, ...,
       LEAD(units, 1) OVER w AS units_next_week
FROM weekly_sales
WINDOW w AS (PARTITION BY product_id, warehouse ORDER BY week_start)
```

The rows of the current week have no `units_next_week` yet. Hermod trains
only on rows with a target, so they can stay in the dataset; they are the
rows to forecast.

**Training.** Target `units_next_week`, the 13 columns, `gradient_boosting`.

**Judging a forecast.** Hermod holds back a random fifth of the rows, so its
test weeks are mixed in among the training weeks. That flatters a forecast
a little. The fair test trains on the past and scores the weeks after it: the
case's test (below) trains on every week but the last 8 and forecasts those
8 weeks, against the forecasts a planner makes without a model. Do the same
before you trust a forecast: train on a dataset that stops some weeks ago,
then compare its predictions with what happened.

**Result.** Hermod's own scores, training on all weeks (40,019 to train,
10,005 to test): MAE 5.79 units, RMSE 9.59, R² 0.914, against a baseline
MAE of 21.58. Training on all but the last 8 weeks and forecasting those
8 weeks (3,848 rows):

| Forecast for the last 8 weeks | MAE (units) |
|---|---|
| Model (`gradient_boosting`) | **5.89** |
| The mean of the last 4 weeks | 8.17 |
| Same as last week | 9.90 |

**Reading it.** On weeks it never saw, the model's average miss is 28%
smaller than a 4-week moving average and 40% smaller than "same as last
week", because it knows what the averages cannot: next week's promotion,
price and holiday. Importance: `avg_units_last_13_weeks` 0.56,
`avg_units_last_4_weeks` 0.25, then `discount_next_week`,
`holiday_next_week` and `promo_next_week`.

**Using it.** The same week of the same series, without and with a promotion
and a holiday:

```bash
curl -X POST https://hermod.example.com/api/ml/serve/default/weekly_demand \
  -H "X-API-Key: hml_…" -H 'Content-Type: application/json' -d '{"instances": [
  {"warehouse": "north", "category": "beverages", "week_of_year_next": 22, "holiday_next_week": true,
   "promo_next_week": true, "discount_next_week": 0.2, "price_next_week": 3.96, "units_last_week": 41,
   "units_2_weeks_ago": 38, "avg_units_last_4_weeks": 40.25, "avg_units_last_13_weeks": 37.6,
   "same_week_last_year": 55, "stockout_last_week": false},
  {"warehouse": "north", "category": "beverages", "week_of_year_next": 23, "holiday_next_week": false,
   "promo_next_week": false, "discount_next_week": 0.0, "price_next_week": 4.95, "units_last_week": 41,
   "units_2_weeks_ago": 38, "avg_units_last_4_weeks": 40.25, "avg_units_last_13_weeks": 37.6,
   "same_week_last_year": 36, "stockout_last_week": false}]}'
```

```json
{"model":"weekly_demand","predictions":[{"value":89.1722070687616},{"value":35.07307602200809}]}
```

For every series at once, send the current week's rows as a
[job](ml.md#asynchronous-jobs) (`"respond": "async"`; a sync call takes at
most 1000 rows), or score them in a weekly workflow: a Cron trigger, a SQL
source running the query above for the latest week, a **Predict** node, and
a sink writing the forecasts to the planning table. Retrain weekly
([Retraining](ml.md#retraining)) so the season and trend stay current.

## 5. Card fraud (one in a hundred)

*Which card payments should be held for review?*

**Data.** [`card_fraud.py`](../ml/examples/cases/card_fraud.py): 100,000
payments over three months, 1.1% fraud, labelled afterwards. Fraud shows up
as a pattern, none decisive alone: a burst of payments in the last hour, far
from home, abroad, a new device, a failed CVV check, a risky merchant group
at night, an amount far above the card's usual. Columns that do not apply
are empty: an in-store payment has no device or CVV check (67% empty), an
online one no distance from home (33% empty).

`tx_count_last_1h` and `amount_anomaly_score` are what the
[Rolling Features](ml.md#rolling-features) and
[Anomaly Score](ml.md#anomaly-score) nodes compute per card in a workflow:
the anomaly score is the amount's z-score against the card's recent
payments, empty until the card has ten. Computing them in the same workflow
that scores live payments keeps training and serving alike.

| Keep | Drop |
|---|---|
| `hour`, `amount`, `merchant_group`, `channel`, `country`, `card_country`, `is_foreign`, `card_age_days`, `tx_count_last_1h`, `amount_anomaly_score` (8% empty), `distance_from_home_km` (empty online), `new_device` and `cvv_failed` (empty in store) | `tx_id` (ID), `tx_time` (time stamp; the hour is its own column), `card_id` and `merchant_id` (high-cardinality), `chargeback_filed` (filed weeks later: it is the label; flagged) |

The check flags `new_device` and `cvv_failed` as `mostly_empty`, and the
version repeats the warning. Keep them: they are empty by design, and when
they are filled they matter.

**Training.** Target `is_fraud`, the 13 columns, `gradient_boosting` on the
built-in engine.

**Result** (80,000 to train, 20,000 to test):

| | accuracy | balanced accuracy | F1 | ROC AUC | Engine |
|---|---|---|---|---|---|
| `gradient_boosting` | 0.913 | **0.872** | 0.565 | 0.946 | built-in, about 5 s |
| `linear` | 0.878 | 0.841 | 0.531 | 0.926 | built-in |
| `hist_gradient_boosting` | 0.940 | 0.868 | 0.598 | **0.959** | worker |
| `linear` | 0.875 | 0.853 | 0.531 | 0.932 | worker |
| `random_forest` | 0.990 | 0.538 | 0.567 | 0.922 | worker |
| `xgboost` | 0.985 | 0.758 | 0.718 | 0.917 | worker, about 11 s |
| Baseline (always "no") | 0.989 | 0.500 | 0.497 | 0.500 | |

(The worker and the built-in engine split the rows differently, so compare
within an engine. The worker's `hist_gradient_boosting` took close to three
minutes on the 4-core machine these ran on, most of it scoring importance.)

**Reading it.** Every model is *less* accurate than "always no", and that is
the point: the baseline lets every fraud through. The built-in
`gradient_boosting` catches most frauds (balanced accuracy 0.872) and ranks
a fraud above an honest payment 95% of the time. To do it, its label also
says `yes` to some honest payments (its accuracy is 0.913, against 0.989 for
"always no"), because the classes are weighed equally. That cut is the
model's, not yours: `random_forest` and `xgboost` rank nearly as well
(ROC AUC 0.92) but rarely say `yes`, so their labels miss most fraud. With
fraud, act on the `probability`, and set the cut-off from how many reviews
your team can do. Importance (built-in): `tx_count_last_1h` 0.49,
`distance_from_home_km` 0.30, `is_foreign` 0.10, `amount` 0.05.

**Using it.**

```bash
curl -X POST https://hermod.example.com/api/ml/serve/default/card_fraud \
  -H "X-API-Key: hml_…" -H 'Content-Type: application/json' -d '{"instances": [
  {"hour": 2, "amount": 1240.0, "merchant_group": "gift_cards", "channel": "online", "country": "NG",
   "card_country": "ID", "is_foreign": true, "card_age_days": 40, "tx_count_last_1h": 5,
   "amount_anomaly_score": 4.1, "distance_from_home_km": null, "new_device": true, "cvv_failed": true},
  {"hour": 13, "amount": 32.5, "merchant_group": "grocery", "channel": "contactless", "country": "ID",
   "card_country": "ID", "is_foreign": false, "card_age_days": 1900, "tx_count_last_1h": 0,
   "amount_anomaly_score": 0.2, "distance_from_home_km": 3.2, "new_device": null, "cvv_failed": null}]}'
```

```json
{"model":"card_fraud","predictions":[{"label":"yes","probability":0.9985749193192356},{"label":"no","probability":0.9829936157247032}]}
```

`probability` is the probability of the answer given in `label`: here the
second payment is 98% likely honest, so 2% fraud. In a workflow, this is
the flow at the top of [ml.md](ml.md): payments from CDC, Rolling Features and
Anomaly Score per card, a **Predict** node, and a Router that holds a payment
for review when the prediction is `yes` with a probability over your cut-off,
and lets the rest through.

## 6. Account spend (a neural network on 80,000 rows)

*What will each customer account be billed next quarter, for the revenue
forecast and to spot accounts about to shrink?*

**Data.** [`account_spend.py`](../ml/examples/cases/account_spend.py):
80,000 account-quarters of a subscription software vendor. Spend is seats
times the price per seat, plus usage fees, and next quarter's moves with how
the account uses the product: seats grow where most users are active and log
in often, and shrink or churn (7.5% spend nothing next quarter) where usage
fades and support tickets pile up. These are smooth effects that multiply
each other, the shape a neural network suits. Spend is very skewed: half the
accounts spend under 605, the largest over 300,000.

| Keep | Drop |
|---|---|
| `plan`, `region`, `industry`, `months_as_customer`, `seats`, `price_per_seat`, `discount`, `spend_this_quarter`, `spend_growth_qoq`, `active_user_share`, `logins_per_user_30d`, `api_calls_30d`, `support_tickets_90d` | `account_id` (ID), `quarter_end` (date as text), `invoiced_next_quarter` (what finance billed: the target under another name; flagged), `nps` (72% empty: flagged `mostly_empty`; few accounts answer the survey) |

**Training.** Target `spend_next_quarter`, the 13 columns, algorithm `mlp`
(the built-in engine's network) with these settings, under **Advanced** or in
`params`:

```json
{"dataset": "account_spend", "target": "spend_next_quarter", "algorithm": "mlp",
 "features": ["plan", "region", "industry", "months_as_customer", "seats", "price_per_seat", "discount",
              "spend_this_quarter", "spend_growth_qoq", "active_user_share", "logins_per_user_30d", "api_calls_30d",
              "support_tickets_90d"],
 "params": {"hidden_layers": [128, 64, 32], "epochs": 300, "batch_size": 256, "learning_rate": 0.001, "patience": 20}}
```

On 64,000 training rows the defaults (batch 32) take many more steps per
epoch; a batch of 256 trains faster, and a patience of 20 lets it run past
noisy epochs. Three layers give it room for the interactions.

**Result** (64,000 to train, 16,000 to test, built-in engine):

| | MAE | RMSE | R² | Time |
|---|---|---|---|---|
| `mlp` (settings above) | **625** | 3,362 | 0.936 | about 75 s |
| `gradient_boosting` | 637 | 3,269 | **0.939** | about 5 s |
| `random_forest` | 626 | 3,461 | 0.932 | about 67 s |
| `linear` | 861 | 3,560 | 0.928 | about 2 s |
| Baseline (the average) | 6,427 | 13,290 | 0.000 | |

On the worker (its own split; baseline MAE 6,456): `keras_mlp` with the same
settings R² 0.956 and MAE 1,003 in 22 s; `hist_gradient_boosting` R² 0.911
and MAE 670. `pytorch_mlp` takes the same `params`; it was not run for this
guide.

**Reading it.** The network and gradient boosting are level, both cutting
the average miss by about 90%; the network takes about fifteen times longer
to train. That is the usual finding on business tables: try
`gradient_boosting` first, and keep a network when it wins on your data or
when you need what only it gives (on the worker, a GPU pool for very large
data). Importance: `spend_this_quarter` 0.64 and `seats` 0.29 carry most of
it; the usage columns decide the direction for each account. A heavily
skewed target makes RMSE and R² follow the few largest accounts: read MAE,
and check the large accounts on their own.

**Using it.** The same account, healthy and fading:

```bash
curl -X POST https://hermod.example.com/api/ml/serve/default/account_spend \
  -H "X-API-Key: hml_…" -H 'Content-Type: application/json' -d '{"instances": [
  {"plan": "business", "region": "emea", "industry": "finance", "months_as_customer": 30, "seats": 40,
   "price_per_seat": 42.3, "discount": 0.06, "spend_this_quarter": 5076.0, "spend_growth_qoq": 0.05,
   "active_user_share": 0.9, "logins_per_user_30d": 22.0, "api_calls_30d": 3500, "support_tickets_90d": 1},
  {"plan": "business", "region": "emea", "industry": "finance", "months_as_customer": 30, "seats": 40,
   "price_per_seat": 42.3, "discount": 0.06, "spend_this_quarter": 5076.0, "spend_growth_qoq": -0.1,
   "active_user_share": 0.25, "logins_per_user_30d": 3.0, "api_calls_30d": 3500, "support_tickets_90d": 6}]}'
```

```json
{"model":"account_spend","predictions":[{"value":5843.343095567678},{"value":1982.9981895538658}]}
```

The fading account is expected to spend 1,983 against 5,076 now: one for the
account manager this week.

## Reproducing the numbers

The built-in engine's numbers come from the Go tests of the cases
(`pkg/ml/builtin/cases_test.go`), run on the full-size files; the worker's
from its tests (`ml/worker/tests/test_cases.py`) and the same trainings sent
to the worker. Training is seeded (42), so the same file and settings give
the same version on the same engine.

```bash
mkdir -p /tmp/cases
for c in late_payment ticket_routing delivery_time weekly_demand account_spend card_fraud; do
  python3 ml/examples/cases/$c.py --out /tmp/cases/$c.csv
done
HERMOD_ML_CASES_DIR=/tmp/cases go test -count=1 -v -run 'TestCase' ./pkg/ml/builtin/
```

Without `HERMOD_ML_CASES_DIR` the Go tests train on the samples
(card fraud is skipped: 1,300 payments hold about 14 frauds), which is what CI
runs; the numbers are lower there, and the tests only ask each model to beat
its baseline by a margin. After changing a generator, rewrite its sample
with `python3 ml/examples/cases/<case>.py --sample`; the worker's tests fail
until the sample matches the generator.
