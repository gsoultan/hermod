# Recommendation cases

Three worked examples of [recommendation scenarios](ml.md#recommendation-scenarios)
on businesses more complex than a single orders table: a consumer goods
distributor, an industrial supplier and a pharmacy chain. Each one says what
the business wants to know, which of its tables and columns to map, which
scenarios to train, what to ask, what came back, and what each extra signal
changed.

The data is synthetic and follows from a seed, so every number and answer
below can be reproduced. `ml/worker/tests/cases.py` generates the tables:

```bash
python -I ml/worker/tests/cases.py /tmp/cases                 # every table of the three cases as CSV
python -I ml/worker/tests/cases.py /tmp/cases fmcg            # one case
python -I ml/worker/tests/cases.py --sample 12 /tmp/cases     # the first 12 rows of each table
```

The first 12 rows of each table are in `ml/examples/recommendation-cases/`,
to see their shape without running anything.

Upload each CSV as a dataset of the same name (or point a dataset at the same
tables in your database), then train with the mappings below. The answers
were produced by training each scenario with the worker on the full tables
and asking the trained model as Hermod serves it, as of 2026-10-01 (the day
after the last order). They are the `results` of the answer of
`POST /api/vhosts/{vhost}/ml/models/{name}/recommend`; the answer also
carries its `recommendation_id`. Scores are as the server prints them.

`ml/worker/tests/test_recommendation_cases.py` trains every scenario of every case and checks
what this page says each one does; `pkg/ml/recommend/cases_test.go` and the
`_industrial` and `_pharmacy` parity fixtures check that Hermod serves the
same answers the worker computed.

- [1. A consumer goods distributor](#1-a-consumer-goods-distributor)
- [2. An industrial supplier](#2-an-industrial-supplier)
- [3. A pharmacy chain](#3-a-pharmacy-chain)
- [What each signal changed](#what-each-signal-changed)

## 1. A consumer goods distributor

**The business.** A distributor sells about 150 fast-moving products (tea,
syrup, noodles, cooking oil, dates, detergent, medicine, vitamins, diapers)
to 300 outlets in Jakarta, Bandung and Surabaya: minimarkets, restaurants and
pharmacies, each in one of three neighbourhoods of its city. Four salesmen
cover them, two in Jakarta. Every outlet reorders a basket on its own rhythm
(weekly to monthly), and a neighbourhood's outlets of one kind share favourite
products. Syrups, dates and cooking oil sell three times as much in the six
weeks around Ramadan (4 February to 19 March 2026). A product of each
category was discontinued at the end of August and another is out of stock,
each with a substitute, and one was launched between late July and
mid-September. Some outlets
sent back most of a product, and the salesmen lost some quotes and have
others open. 12 months of history.

**The questions.** Which outlets should each salesman visit this week? What
should we offer an outlet, and why? Which outlets are about to reorder, and
what? Who should hear about the new tea? The syrup is out of stock: what
else?

**Tables and columns.** Seven datasets, mapped like this in the training
body's `data` (the console guesses most of these from the column names):

| Role | Dataset | Columns mapped |
|---|---|---|
| Orders (`transactions`) | `orders` | customer `outlet_id`, product `sku`, date `order_date`, quantity `qty`, amount `amount` |
| Customers | `outlets` | id `outlet_id`, salesman `salesman_id`, territory `city`, category (`segment`) `outlet_type`, size `outlet_size`, channel `channel`, region `city`, latitude `latitude`, longitude `longitude`, credit hold `credit_hold`, name `name` |
| Products | `products` | id `sku`, category, subcategory, brand, price, margin, pack size, unit, launch date, end date, stock, substitute, active (each the column of the same name), name |
| Visits | `visits` | customer `outlet_id`, date `visit_date`, salesman `salesman_id`, outcome |
| Salesmen | `salesmen` | id `salesman_id`, visits per day, working days |
| Returns | `returns` | customer `outlet_id`, product `sku`, date `return_date`, quantity `qty` |
| Quotes | `quotes` | customer `outlet_id`, product `sku`, date `quote_date`, outcome `status`, reason `lost_reason` |

`city` is both the territory (which rules can keep a salesman to) and the
region (which makes peers). The outlets' type, size, channel and city make
their peer key; with coordinates, an outlet's peers are the nearest ones of
the same key within 5 km.

**Train.** One model per question, the same `data` for each:

```json
{"task": "recommendation", "scenario": "products", "data": { … the seven roles above … }}
```

| Model | Scenario | Measured on the held-back last period | Against |
|---|---|---|---|
| `fmcg_visits` | visits | `ordered_in_period@k` 0.550, `revenue_share@k` 1.000 | most overdue first: 0.534, 0.948; biggest first: 0.471, 0.933; most popular: 0.496, 0.932 |
| `fmcg_products` | products | `hit_rate@10` 0.140, `ndcg@10` 0.045 | most popular: 0.063, 0.021 |
| `fmcg_reorder` | reorder | `mae_days` 1.93, `on_time_share` 0.993 | everyone's usual gap: 6.72, 0.593 |
| `fmcg_audience` | audience | `hit_rate@10` 0.140, `ndcg@10` 0.045 | most popular: 0.063, 0.021 |
| `fmcg_similar` | similar | `hit_rate@10` 0.140, `ndcg@10` 0.045 | most popular: 0.063, 0.021 |

Products, audience and similar share one evaluation (a customer's products
over the last 30 days), so they show the same numbers. The health check is
amber on `few_customers` only: 300 customers is under the 1,000 the product
scenarios would like.

**A salesman's visits.** SM3 covers Bandung:

`POST /api/vhosts/default/ml/models/fmcg_visits/recommend`

```json
{"for": {"salesman": "SM3"}, "period": {"from": "2026-10-01", "to": "2026-10-02"}, "k": 3}
```

```json
{"for": "SM3",
 "items": [
  {"id":"OUT163","score":0.7083333134651184,"rank":1,"reasons":[{"code":"reorder_due","text":"Usually orders every 7 days; last order 6 days ago"},{"code":"high_value","text":"Top 3% of customers by revenue"},{"code":"not_visited","text":"Not visited for 17 days (target every 7)"}],"fields":{"channel":"Modern trade","lat":-6.923160076141357,"lon":107.61788940429688,"name":"Minimarket Bandung 43","region":"Bandung","salesman":"SM3","segment":"Minimarket","size":"large","territory":"Bandung"}},
  {"id":"OUT124","score":0.6506666541099548,"rank":2,"reasons":[{"code":"reorder_due","text":"Usually orders every 14 days; last order 13 days ago"},{"code":"high_value","text":"Top 20% of customers by revenue"},{"code":"not_visited","text":"Not visited for 20 days (target every 15)"},{"code":"whitespace","text":"Similar customers buy 9 products this customer has never bought"}],"fields":{"channel":"HoReCa","lat":-6.926809787750244,"lon":107.62715148925781,"name":"Restaurant Bandung 4","region":"Bandung","salesman":"SM3","segment":"Restaurant","size":"medium","territory":"Bandung"}},
  {"id":"OUT151","score":0.6170476078987122,"rank":3,"reasons":[{"code":"reorder_due","text":"Usually orders every 7 days; last order 5 days ago"},{"code":"high_value","text":"Top 4% of customers by revenue"},{"code":"not_visited","text":"Not visited for 6 days (target every 7)"}],"fields":{"channel":"Pharmacy","lat":-6.917689800262451,"lon":107.60687255859375,"name":"Pharmacy Bandung 31","region":"Bandung","salesman":"SM3","segment":"Pharmacy","size":"large","territory":"Bandung"}}],
 "plan": [
  {"date": "2026-10-01", "items": [
    {"id":"OUT163","score":0.7083333134651184,"rank":1,"reasons":[{"code":"reorder_due","text":"Usually orders every 7 days; last order 6 days ago"},{"code":"high_value","text":"Top 3% of customers by revenue"},{"code":"not_visited","text":"Not visited for 17 days (target every 7)"}],"fields":{"channel":"Modern trade","lat":-6.923160076141357,"lon":107.61788940429688,"name":"Minimarket Bandung 43","region":"Bandung","salesman":"SM3","segment":"Minimarket","size":"large","territory":"Bandung"}},
    {"id":"OUT124","score":0.6506666541099548,"rank":2,"reasons":[{"code":"reorder_due","text":"Usually orders every 14 days; last order 13 days ago"},{"code":"high_value","text":"Top 20% of customers by revenue"},{"code":"not_visited","text":"Not visited for 20 days (target every 15)"},{"code":"whitespace","text":"Similar customers buy 9 products this customer has never bought"}],"fields":{"channel":"HoReCa","lat":-6.926809787750244,"lon":107.62715148925781,"name":"Restaurant Bandung 4","region":"Bandung","salesman":"SM3","segment":"Restaurant","size":"medium","territory":"Bandung"}},
    {"id":"OUT151","score":0.6170476078987122,"rank":3,"reasons":[{"code":"reorder_due","text":"Usually orders every 7 days; last order 5 days ago"},{"code":"high_value","text":"Top 4% of customers by revenue"},{"code":"not_visited","text":"Not visited for 6 days (target every 7)"}],"fields":{"channel":"Pharmacy","lat":-6.917689800262451,"lon":107.60687255859375,"name":"Pharmacy Bandung 31","region":"Bandung","salesman":"SM3","segment":"Pharmacy","size":"large","territory":"Bandung"}}]}]}
```

The plan is the list ordered as a route for each working day; with three
customers all fit on the first day.

**What to offer an outlet.** Minimarket Jakarta 17 (`OUT017`), a medium
general-trade minimarket:

`POST /api/vhosts/default/ml/models/fmcg_products/recommend`

```json
{"for": {"customer": "OUT017"}, "k": 3}
```

```json
{"for": "OUT017", "summary": "Minimarket · medium · General trade · Jakarta customers within 5 km buy: Nusantara Syrup 10, Sedap Rasa Dates 2, Sedap Rasa Dates 8",
 "items": [
  {"id":"FM010","score":0.6455638408660889,"rank":1,"reasons":[{"code":"similar_customers","text":"Bought by 16 of 20 similar customers"},{"code":"peer_group","text":"Bought by 4 of 6 Minimarket · medium · General trade · Jakarta customers within 5 km"},{"code":"brand","text":"Buys a lot of Nusantara (25% of spend)"}],"fields":{"brand":"Nusantara","category":"Beverages","name":"Nusantara Syrup 10","pack_size":12,"subcategory":"Syrup","unit":"carton"}},
  {"id":"FM077","score":0.6413029432296753,"rank":2,"reasons":[{"code":"similar_customers","text":"Bought by 16 of 20 similar customers"},{"code":"peer_group","text":"Bought by 3 of 6 Minimarket · medium · General trade · Jakarta customers within 5 km"},{"code":"brand","text":"Buys a lot of Sedap Rasa (23% of spend)"}],"fields":{"brand":"Sedap Rasa","category":"Cooking","name":"Sedap Rasa Dates 2","pack_size":24,"subcategory":"Dates","unit":"box"}},
  {"id":"FM083","score":0.5895833969116211,"rank":3,"reasons":[{"code":"similar_customers","text":"Bought by 12 of 20 similar customers"},{"code":"peer_group","text":"Bought by 4 of 6 Minimarket · medium · General trade · Jakarta customers within 5 km"},{"code":"brand","text":"Buys a lot of Sedap Rasa (23% of spend)"}],"fields":{"brand":"Sedap Rasa","category":"Cooking","name":"Sedap Rasa Dates 8","pack_size":24,"subcategory":"Dates","unit":"box"}}]}
```

Its peers are the six nearest medium general-trade minimarkets in Jakarta
within 5 km, and `summary` says in a line what they buy.

**A new product, quoted.** Minimarket Bandung 87 (`OUT207`) was quoted the new
Mawar Tea 15 on 2026-09-22 and has not ordered it:

`POST /api/vhosts/default/ml/models/fmcg_products/recommend`

```json
{"for": {"customer": "OUT207"}, "k": 3}
```

```json
{"for": "OUT207", "summary": "Minimarket · small · General trade · Bandung customers within 5 km buy: Pelita Dates 6, Mawar Tea 15, Mawar Tea 9",
 "items": [
  {"id":"FM081","score":0.6985386610031128,"rank":1,"reasons":[{"code":"similar_customers","text":"Bought by 17 of 20 similar customers"},{"code":"peer_group","text":"Bought by 6 of 9 Minimarket · small · General trade · Bandung customers within 5 km"}],"fields":{"brand":"Pelita","category":"Cooking","name":"Pelita Dates 6","pack_size":40,"subcategory":"Dates","unit":"carton"}},
  {"id":"FM015","score":0.39454513788223267,"rank":2,"reasons":[{"code":"similar_customers","text":"Bought by 6 of 20 similar customers"},{"code":"peer_group","text":"Bought by 2 of 9 Minimarket · small · General trade · Bandung customers within 5 km"},{"code":"brand","text":"Buys a lot of Mawar (22% of spend)"},{"code":"new_product","text":"New, launched 2026-09-10"},{"code":"quoted","text":"Quoted on 2026-09-22, not ordered yet"}],"fields":{"brand":"Mawar","category":"Beverages","name":"Mawar Tea 15","pack_size":40,"subcategory":"Tea","unit":"carton"}},
  {"id":"FM009","score":0.3604061007499695,"rank":3,"reasons":[{"code":"similar_customers","text":"Bought by 6 of 20 similar customers"},{"code":"peer_group","text":"Bought by 2 of 9 Minimarket · small · General trade · Bandung customers within 5 km"},{"code":"brand","text":"Buys a lot of Mawar (22% of spend)"}],"fields":{"brand":"Mawar","category":"Beverages","name":"Mawar Tea 9","pack_size":40,"subcategory":"Tea","unit":"carton"}}]}
```

Without the quotes role the tea was fifth on this outlet's list of ten; the
open quote moves it to second.

**Who is about to reorder.** SM3's outlets due within three days, overdue
ones marked, each with the products due and a quantity in whole packs:

`POST /api/vhosts/default/ml/models/fmcg_reorder/recommend`

```json
{"for": {"salesman": "SM3"}, "within_days": 3, "k": 2}
```

```json
{"for": "SM3",
 "items": [
  {"id":"OUT142","score":1,"rank":1,"reasons":[{"code":"reorder_due","text":"Usually orders every 20 days; last order 25 days ago"}],"fields":{"channel":"HoReCa","lat":-6.911640167236328,"lon":107.625732421875,"name":"Restaurant Bandung 22","region":"Bandung","salesman":"SM3","segment":"Restaurant","size":"small","territory":"Bandung"},"due_date":"2026-09-26","overdue":true,"products":[{"id":"FM002","due_date":"2026-07-29","overdue":true,"fields":{"brand":"Kapal Api Jaya","category":"Beverages","name":"Kapal Api Jaya Syrup 2","pack_size":24,"subcategory":"Syrup","suggested_qty":1,"suggested_text":"1 box (6 pcs)","unit":"box"}},{"id":"FM095","due_date":"2026-09-18","overdue":true,"fields":{"brand":"Pelita","category":"Frozen","name":"Pelita Nuggets 5","pack_size":24,"subcategory":"Nuggets","suggested_qty":1,"suggested_text":"1 box (3 pcs)","unit":"box"}},{"id":"FM003","due_date":"2026-09-29","overdue":true,"fields":{"brand":"Mawar","category":"Beverages","name":"Mawar Tea 3","pack_size":40,"subcategory":"Tea","suggested_qty":1,"suggested_text":"1 carton (3 pcs)","unit":"carton"}},{"id":"FM009","due_date":"2026-09-29","overdue":true,"fields":{"brand":"Mawar","category":"Beverages","name":"Mawar Tea 9","pack_size":40,"subcategory":"Tea","suggested_qty":1,"suggested_text":"1 carton (3 pcs)","unit":"carton"}}]},
  {"id":"OUT173","score":1,"rank":2,"reasons":[{"code":"reorder_due","text":"Usually orders every 7 days; last order 9 days ago"}],"fields":{"channel":"Pharmacy","lat":-6.850679874420166,"lon":107.64813995361328,"name":"Pharmacy Bandung 53","region":"Bandung","salesman":"SM3","segment":"Pharmacy","size":"small","territory":"Bandung"},"due_date":"2026-09-29","overdue":true,"products":[{"id":"FM124","due_date":"2026-09-29","overdue":true,"fields":{"brand":"Samudra","category":"Vitamins","name":"Samudra Multivitamin 4","pack_size":12,"subcategory":"Multivitamin","suggested_qty":1,"suggested_text":"1 carton (3 pcs)","unit":"carton"}},{"id":"FM126","due_date":"2026-09-29","overdue":true,"fields":{"brand":"Kapal Api Jaya","category":"Vitamins","name":"Kapal Api Jaya Multivitamin 6","pack_size":40,"subcategory":"Multivitamin","suggested_qty":1,"suggested_text":"1 carton (2 pcs)","unit":"carton"}},{"id":"FM131","due_date":"2026-09-29","overdue":true,"fields":{"brand":"Sedap Rasa","category":"Vitamins","name":"Sedap Rasa Vitamin C 11","pack_size":24,"subcategory":"Vitamin C","suggested_qty":1,"suggested_text":"1 box (3 pcs)","unit":"box"}},{"id":"FM141","due_date":"2026-09-29","overdue":true,"fields":{"brand":"Mawar","category":"Baby care","name":"Mawar Baby soap 6","pack_size":40,"subcategory":"Baby soap","suggested_qty":1,"suggested_text":"1 carton (3 pcs)","unit":"carton"}},{"id":"FM130","due_date":"2026-09-30","overdue":true,"fields":{"brand":"Samudra","category":"Vitamins","name":"Samudra Multivitamin 10","pack_size":12,"subcategory":"Multivitamin","suggested_qty":1,"suggested_text":"1 carton (2 pcs)","unit":"carton"}}]}]}
```

**Who should hear about the new tea, in Bandung.** Mawar Tea 15 launched on
2026-09-10, so few have bought it yet; `region` keeps the list to Bandung:

`POST /api/vhosts/default/ml/models/fmcg_audience/recommend`

```json
{"for": {"product": "FM015", "region": "Bandung"}, "k": 3}
```

```json
{"for": "FM015",
 "items": [
  {"id":"OUT207","score":0.47539645433425903,"rank":1,"reasons":[{"code":"similar_customers","text":"6 of 20 similar customers buy it"},{"code":"peer_group","text":"Bought by 2 of 9 Minimarket · small · General trade · Bandung customers within 5 km"},{"code":"new_product","text":"New, launched 2026-09-10"}],"fields":{"channel":"General trade","lat":-6.949389934539795,"lon":107.66036987304688,"name":"Minimarket Bandung 87","region":"Bandung","salesman":"SM3","segment":"Minimarket","size":"small","territory":"Bandung"}},
  {"id":"OUT156","score":0.4637549817562103,"rank":2,"reasons":[{"code":"new_product","text":"New, launched 2026-09-10"}],"fields":{"channel":"HoReCa","lat":-6.9454498291015625,"lon":107.66574096679688,"name":"Restaurant Bandung 36","region":"Bandung","salesman":"SM3","segment":"Restaurant","size":"large","territory":"Bandung"}},
  {"id":"OUT129","score":0.41598597168922424,"rank":3,"reasons":[{"code":"new_product","text":"New, launched 2026-09-10"}],"fields":{"channel":"HoReCa","lat":-6.942019939422607,"lon":107.65918731689453,"name":"Restaurant Bandung 9","region":"Bandung","salesman":"SM3","segment":"Restaurant","size":"large","territory":"Bandung"}}]}
```

**The syrup is out of stock.** Kapal Api Jaya Syrup 14 (`FM014`) has 0 in
stock:

`POST /api/vhosts/default/ml/models/fmcg_similar/recommend`

```json
{"for": {"product": "FM014"}, "k": 3}
```

```json
{"for": "FM014",
 "items": [
  {"id":"FM008","score":0.33041542768478394,"rank":1,"reasons":[{"code":"bought_together","text":"Often bought by the same customers as FM014"},{"code":"same_kind","text":"Same brand and subcategory (Kapal Api Jaya, Syrup)"}],"fields":{"brand":"Kapal Api Jaya","category":"Beverages","name":"Kapal Api Jaya Syrup 8","pack_size":24,"subcategory":"Syrup","unit":"box"}},
  {"id":"FM011","score":0.3023715913295746,"rank":2,"reasons":[{"code":"substitute","text":"Instead of Sedap Rasa Tea 13 (discontinued)"}],"fields":{"brand":"Pelita","category":"Beverages","name":"Pelita Tea 11","pack_size":24,"subcategory":"Tea","unit":"box"}},
  {"id":"FM092","score":0.26821789145469666,"rank":3,"reasons":[{"code":"bought_together","text":"Often bought by the same customers as FM014"},{"code":"same_kind","text":"Same brand (Kapal Api Jaya)"}],"fields":{"brand":"Kapal Api Jaya","category":"Frozen","name":"Kapal Api Jaya Sausages 2","pack_size":24,"subcategory":"Sausages","unit":"box"}}]}
```

Products bought by the same outlets come first. Sedap Rasa Tea 13 was
discontinued on 2026-08-31, so where it would have ranked its substitute
(Pelita Tea 11) is listed with the reason `substitute`. A discontinued or
out-of-stock product is never listed: the tests ask every outlet's products
and every product's similar products, and find none.

## 2. An industrial supplier

**The business.** A supplier of bearings, lubricants, filters, safety gear,
fasteners, hydraulics, tools and electrical parts sells 80 products to 60
accounts in four regions of Java: factories, workshops and contractors, with
37 to 1,395 staff. Each account buys on a contract price tier: Contract A
accounts buy the premium brand lines (Ironbark, Kestrel), list-price accounts
the economy lines (Volta, Saguaro), and Contract B either. They reorder every
45 to 120 days. New business comes through quotes: every account was quoted a
product it did not buy and won it (ordered two weeks later), and half of
them were quoted another in the last 90 days, a quarter of all accounts
losing it (price, lead time, specification) and a quarter still open. One account in ten sent back a wrong-specification
delivery in full. 18 months of history, three key account managers.

**The questions.** Which accounts will order in the next month, and what?
What should we offer an account, without repeating a quote it just turned
down? Which accounts of a tier are due?

**Tables and columns.**

| Role | Dataset | Columns mapped |
|---|---|---|
| Orders | `invoices` | customer `account_no`, product `item_code`, date `invoice_date`, quantity `qty`, amount `net_amount` |
| Customers | `accounts` | id `account_no`, salesman `kam`, territory `region`, category `industry`, price tier `price_tier`, name `account_name` |
| Products | `items` | id `item_code`, category `product_group`, subcategory `product_line`, brand, price `list_price`, pack size, unit, launch date `launched_on`, name `description` |
| Visits | `visits` | customer `account_no`, date `visit_date`, salesman `kam`, outcome |
| Salesmen | `managers` | id `kam`, visits per day, working days |
| Returns | `returns` | customer `account_no`, product `item_code`, date `return_date`, quantity `qty` |
| Quotes | `quotes` | customer `account_no`, product `item_code`, date `quote_date`, outcome `result`, reason `loss_reason` |

The accounts table also has `headcount`, `channel` and `region`, and they are
**not** mapped as peer attributes on purpose. Every attribute mapped joins
the peer key, and 60 accounts split by industry, size, tier, channel and
region make 39 groups, most too
small to count (a peer share needs at least 3 buyers). Industry and price tier
alone make 9 groups, and they are what
decides what an account buys. Measured: `ndcg@10`
0.414 with the two, 0.405 with all
five. `region` stays the territory, for rules.

**Train.**

| Model | Scenario | Measured | Against |
|---|---|---|---|
| `industrial_visits` | visits | `ordered_in_period@k` 0.150, `revenue_share@k` 1.000 | most overdue first: 0.150, 1.000; biggest first: 0.150, 1.000; most popular: 0.150, 1.000 |
| `industrial_products` | products | `hit_rate@10` 0.717, `ndcg@10` 0.414 | most popular: 0.383, 0.131 |
| `industrial_reorder` | reorder | `mae_days` 10.14, `on_time_share` 0.864 | everyone's usual gap: 23.49, 0.508 |
| `industrial_audience` | audience | `hit_rate@10` 0.717, `ndcg@10` 0.414 | most popular: 0.383, 0.131 |
| `industrial_similar` | similar | `hit_rate@10` 0.717, `ndcg@10` 0.414 | most popular: 0.383, 0.131 |

The visit plan ties its baselines: three managers at 4 visits a day for 5
days can visit all 60 accounts in the week planned, so every list holds the
same accounts and only their order differs. The reorder model is off by
about 10 days on cycles of 45 to 120, against 23 for everyone's usual gap.

**Contract A accounts due in the next 30 days.**

`POST /api/vhosts/default/ml/models/industrial_reorder/recommend`

```json
{"for": {"all_customers": true, "price_tier": "Contract A"}, "within_days": 30, "k": 3}
```

```json
{"for": "all_customers",
 "items": [
  {"id":"ACC01","score":1,"rank":1,"reasons":[{"code":"reorder_due","text":"Usually orders every 90 days; last order 134 days ago"}],"fields":{"name":"Factory 1","price_tier":"Contract A","salesman":"KAM1","segment":"Factory","territory":"West Java"},"due_date":"2026-08-19","overdue":true,"products":[{"id":"IN001","due_date":"2026-09-25","overdue":true,"fields":{"brand":"Ironbark","category":"Bearings","name":"Ironbark bearings 1","pack_size":1,"subcategory":"Bearings premium","suggested_qty":66,"suggested_text":"66 pieces (66 pcs)","unit":"piece"}},{"id":"IN015","due_date":"2026-09-25","overdue":true,"fields":{"brand":"Kestrel","category":"Lubricants","name":"Kestrel lubricants 5","pack_size":10,"subcategory":"Lubricants premium","suggested_qty":6,"suggested_text":"6 boxs (51 pcs)","unit":"box"}},{"id":"IN054","due_date":"2026-09-25","overdue":true,"fields":{"brand":"Kestrel","category":"Hydraulics","name":"Kestrel hydraulics 4","pack_size":1,"subcategory":"Hydraulics premium","suggested_qty":56,"suggested_text":"56 pieces (56 pcs)","unit":"piece"}}]},
  {"id":"ACC57","score":1,"rank":2,"reasons":[{"code":"reorder_due","text":"Usually orders every 106 days; last order 124 days ago"}],"fields":{"name":"Contractor 57","price_tier":"Contract A","salesman":"KAM3","segment":"Contractor","territory":"East Java"},"due_date":"2026-09-13","overdue":true,"products":[{"id":"IN063","due_date":"2026-08-29","overdue":true,"fields":{"brand":"Ironbark","category":"Tools","name":"Ironbark tools 3","pack_size":50,"subcategory":"Tools premium","suggested_qty":2,"suggested_text":"2 cases (54 pcs)","unit":"case"}},{"id":"IN045","due_date":"2026-09-18","overdue":true,"fields":{"brand":"Ironbark","category":"Fasteners","name":"Ironbark fasteners 5","pack_size":10,"subcategory":"Fasteners premium","suggested_qty":6,"suggested_text":"6 boxs (53 pcs)","unit":"box"}},{"id":"IN042","due_date":"2026-09-24","overdue":true,"fields":{"brand":"Ironbark","category":"Fasteners","name":"Ironbark fasteners 2","pack_size":10,"subcategory":"Fasteners premium","suggested_qty":4,"suggested_text":"4 boxs (38 pcs)","unit":"box"}}]},
  {"id":"ACC48","score":1,"rank":3,"reasons":[{"code":"reorder_due","text":"Usually orders every 108 days; last order 107 days ago"}],"fields":{"name":"Contractor 48","price_tier":"Contract A","salesman":"KAM3","segment":"Contractor","territory":"Central Java"},"due_date":"2026-10-02","products":[{"id":"IN034","due_date":"2026-07-02","overdue":true,"fields":{"brand":"Kestrel","category":"Safety","name":"Kestrel safety 4","pack_size":1,"subcategory":"Safety premium","suggested_qty":91,"suggested_text":"91 pieces (91 pcs)","unit":"piece"}},{"id":"IN041","due_date":"2026-10-08","fields":{"brand":"Ironbark","category":"Fasteners","name":"Ironbark fasteners 1","pack_size":1,"subcategory":"Fasteners premium","suggested_qty":49,"suggested_text":"49 pieces (49 pcs)","unit":"piece"}}]}]}
```

`within_days` keeps the accounts due before 2026-10-31, overdue ones
included and marked; `price_tier` keeps one tier.

**What to offer an account.** `ACC04`, a Contract B factory, was quoted
Ironbark bearings 2 (`IN002`) on 2026-09-17:

`POST /api/vhosts/default/ml/models/industrial_products/recommend`

```json
{"for": {"customer": "ACC04"}, "k": 3}
```

```json
{"for": "ACC04",
 "items": [
  {"id":"IN074","score":0.6211569309234619,"rank":1,"reasons":[{"code":"similar_customers","text":"Bought by 5 of 18 similar customers"},{"code":"peer_group","text":"Bought by 3 of 6 Factory · Contract B customers"},{"code":"brand","text":"Buys a lot of Kestrel (79% of spend)"}],"fields":{"brand":"Kestrel","category":"Electrical","name":"Kestrel electrical 4","pack_size":1,"subcategory":"Electrical premium","unit":"piece"}},
  {"id":"IN002","score":0.6052226424217224,"rank":2,"reasons":[{"code":"peer_group","text":"Bought by 2 of 6 Factory · Contract B customers"},{"code":"brand","text":"Buys a lot of Ironbark (21% of spend)"},{"code":"quoted","text":"Quoted on 2026-09-17, not ordered yet"}],"fields":{"brand":"Ironbark","category":"Bearings","name":"Ironbark bearings 2","pack_size":10,"subcategory":"Bearings premium","unit":"box"}},
  {"id":"IN012","score":0.5392631888389587,"rank":3,"reasons":[{"code":"similar_customers","text":"Bought by 4 of 18 similar customers"},{"code":"peer_group","text":"Bought by 2 of 6 Factory · Contract B customers"},{"code":"brand","text":"Buys a lot of Kestrel (79% of spend)"}],"fields":{"brand":"Kestrel","category":"Lubricants","name":"Kestrel lubricants 2","pack_size":10,"subcategory":"Lubricants premium","unit":"box"}}]}
```

Its peers are the other Contract B factories. The open quote ranks second,
one place higher than without the quotes role, with the quote's date.

**A lost quote.** `ACC02`, a Contract A workshop, lost a quote for Ironbark
tools 2 (`IN062`) on 2026-09-09 (lead time). Without the quotes role:

`POST /api/vhosts/default/ml/models/industrial_products_plain/recommend`

```json
{"for": {"customer": "ACC02"}, "k": 3}
```

```json
{"for": "ACC02",
 "items": [
  {"id":"IN042","score":0.6220468878746033,"rank":1,"reasons":[{"code":"similar_customers","text":"Bought by 6 of 18 similar customers"},{"code":"peer_group","text":"Bought by 2 of 6 Workshop · Contract A customers"},{"code":"brand","text":"Buys a lot of Ironbark (85% of spend)"}],"fields":{"brand":"Ironbark","category":"Fasteners","name":"Ironbark fasteners 2","pack_size":10,"subcategory":"Fasteners premium","unit":"box"}},
  {"id":"IN041","score":0.6033228039741516,"rank":2,"reasons":[{"code":"similar_customers","text":"Bought by 7 of 18 similar customers"},{"code":"peer_group","text":"Bought by 4 of 6 Workshop · Contract A customers"},{"code":"brand","text":"Buys a lot of Ironbark (85% of spend)"}],"fields":{"brand":"Ironbark","category":"Fasteners","name":"Ironbark fasteners 1","pack_size":1,"subcategory":"Fasteners premium","unit":"piece"}},
  {"id":"IN062","score":0.6017571687698364,"rank":3,"reasons":[{"code":"similar_customers","text":"Bought by 4 of 18 similar customers"},{"code":"peer_group","text":"Bought by 2 of 6 Workshop · Contract A customers"},{"code":"brand","text":"Buys a lot of Ironbark (85% of spend)"}],"fields":{"brand":"Ironbark","category":"Tools","name":"Ironbark tools 2","pack_size":10,"subcategory":"Tools premium","unit":"box"}}]}
```

With it:

`POST /api/vhosts/default/ml/models/industrial_products/recommend`

```json
{"for": {"customer": "ACC02"}, "k": 3}
```

```json
{"for": "ACC02",
 "items": [
  {"id":"IN042","score":0.6220468878746033,"rank":1,"reasons":[{"code":"similar_customers","text":"Bought by 6 of 18 similar customers"},{"code":"peer_group","text":"Bought by 2 of 6 Workshop · Contract A customers"},{"code":"brand","text":"Buys a lot of Ironbark (85% of spend)"}],"fields":{"brand":"Ironbark","category":"Fasteners","name":"Ironbark fasteners 2","pack_size":10,"subcategory":"Fasteners premium","unit":"box"}},
  {"id":"IN041","score":0.6033228039741516,"rank":2,"reasons":[{"code":"similar_customers","text":"Bought by 7 of 18 similar customers"},{"code":"peer_group","text":"Bought by 4 of 6 Workshop · Contract A customers"},{"code":"brand","text":"Buys a lot of Ironbark (85% of spend)"}],"fields":{"brand":"Ironbark","category":"Fasteners","name":"Ironbark fasteners 1","pack_size":1,"subcategory":"Fasteners premium","unit":"piece"}},
  {"id":"IN005","score":0.45457038283348083,"rank":3,"reasons":[{"code":"peer_group","text":"Bought by 2 of 6 Workshop · Contract A customers"},{"code":"brand","text":"Buys a lot of Ironbark (85% of spend)"}],"fields":{"brand":"Ironbark","category":"Bearings","name":"Ironbark bearings 5","pack_size":10,"subcategory":"Bearings premium","unit":"box"}}]}
```

**A returned product.** `ACC05` sent back all 13 of the Ironbark bearings 5
(`IN005`) it had bought, the wrong specification. With what it has bought
listed too (`exclude_seen: false`), its offers leave that product out:

`POST /api/vhosts/default/ml/models/industrial_products/recommend`

```json
{"for": {"customer": "ACC05"}, "k": 4, "exclude_seen": false}
```

```json
{"for": "ACC05",
 "items": [
  {"id":"IN061","score":0.6487038135528564,"rank":1,"reasons":[{"code":"similar_customers","text":"Bought by 10 of 13 similar customers"},{"code":"peer_group","text":"Bought by 3 of 6 Workshop · Contract B customers"},{"code":"brand","text":"Buys a lot of Ironbark (70% of spend)"}],"fields":{"brand":"Ironbark","category":"Tools","name":"Ironbark tools 1","pack_size":1,"subcategory":"Tools premium","suggested_qty":6,"suggested_text":"6 pieces (6 pcs)","unit":"piece"}},
  {"id":"IN011","score":0.5395967364311218,"rank":2,"reasons":[{"code":"similar_customers","text":"Bought by 5 of 13 similar customers"},{"code":"brand","text":"Buys a lot of Kestrel (30% of spend)"}],"fields":{"brand":"Kestrel","category":"Lubricants","name":"Kestrel lubricants 1","pack_size":1,"subcategory":"Lubricants premium","unit":"piece"}},
  {"id":"IN042","score":0.48928719758987427,"rank":3,"reasons":[{"code":"similar_customers","text":"Bought by 5 of 13 similar customers"},{"code":"brand","text":"Buys a lot of Ironbark (70% of spend)"}],"fields":{"brand":"Ironbark","category":"Fasteners","name":"Ironbark fasteners 2","pack_size":10,"subcategory":"Fasteners premium","suggested_qty":2,"suggested_text":"2 boxs (13 pcs)","unit":"box"}},
  {"id":"IN041","score":0.4477096498012543,"rank":4,"reasons":[{"code":"similar_customers","text":"Bought by 6 of 13 similar customers"},{"code":"brand","text":"Buys a lot of Ironbark (70% of spend)"}],"fields":{"brand":"Ironbark","category":"Fasteners","name":"Ironbark fasteners 1","pack_size":1,"subcategory":"Fasteners premium","unit":"piece"}}]}
```

**Who else would want those bearings.**

`POST /api/vhosts/default/ml/models/industrial_audience/recommend`

```json
{"for": {"product": "IN005"}, "k": 3, "exclude_seen": false}
```

```json
{"for": "IN005",
 "items": [
  {"id":"ACC47","score":0.5660423636436462,"rank":1,"reasons":[{"code":"similar_customers","text":"4 of 15 similar customers buy it"},{"code":"peer_group","text":"Bought by 2 of 6 Workshop · Contract A customers"}],"fields":{"name":"Workshop 47","price_tier":"Contract A","salesman":"KAM2","segment":"Workshop","territory":"Central Java"}},
  {"id":"ACC38","score":0.5512820482254028,"rank":2,"reasons":[{"code":"bought_together","text":"Buys products often bought with it"}],"fields":{"name":"Workshop 38","price_tier":"Contract A","salesman":"KAM2","segment":"Workshop","territory":"West Java"}},
  {"id":"ACC11","score":0.48574501276016235,"rank":3,"reasons":[{"code":"peer_group","text":"Bought by 2 of 6 Workshop · Contract A customers"}],"fields":{"name":"Workshop 11","price_tier":"Contract A","salesman":"KAM2","segment":"Workshop","territory":"Central Java"}}]}
```

**Products like a bearing.**

`POST /api/vhosts/default/ml/models/industrial_similar/recommend`

```json
{"for": {"product": "IN002"}, "k": 3}
```

```json
{"for": "IN002",
 "items": [
  {"id":"IN001","score":0.6499999761581421,"rank":1,"reasons":[{"code":"bought_together","text":"Often bought by the same customers as IN002"},{"code":"same_kind","text":"Same brand and subcategory (Ironbark, Bearings premium)"}],"fields":{"brand":"Ironbark","category":"Bearings","name":"Ironbark bearings 1","pack_size":1,"subcategory":"Bearings premium","unit":"piece"}},
  {"id":"IN054","score":0.5163977742195129,"rank":2,"reasons":[{"code":"bought_together","text":"Often bought by the same customers as IN002"}],"fields":{"brand":"Kestrel","category":"Hydraulics","name":"Kestrel hydraulics 4","pack_size":1,"subcategory":"Hydraulics premium","unit":"piece"}},
  {"id":"IN074","score":0.4714045226573944,"rank":3,"reasons":[{"code":"bought_together","text":"Often bought by the same customers as IN002"}],"fields":{"brand":"Kestrel","category":"Electrical","name":"Kestrel electrical 4","pack_size":1,"subcategory":"Electrical premium","unit":"piece"}}]}
```

**A key account manager's week.**

`POST /api/vhosts/default/ml/models/industrial_visits/recommend`

```json
{"for": {"salesman": "KAM1"}, "k": 3}
```

```json
{"for": "KAM1",
 "items": [
  {"id":"ACC01","score":0.639569878578186,"rank":1,"reasons":[{"code":"reorder_due","text":"Usually orders every 90 days; last order 134 days ago"},{"code":"high_value","text":"Top 23% of customers by revenue"},{"code":"whitespace","text":"Similar customers buy 2 products this customer has never bought"},{"code":"not_visited","text":"Not visited for 35 days (target every 31)"}],"fields":{"name":"Factory 1","price_tier":"Contract A","salesman":"KAM1","segment":"Factory","territory":"West Java"}},
  {"id":"ACC13","score":0.5112121105194092,"rank":2,"reasons":[{"code":"reorder_due","text":"Usually orders every 110 days; last order 107 days ago"},{"code":"whitespace","text":"Similar customers buy 2 products this customer has never bought"},{"code":"not_visited","text":"Not visited for 29 days (target every 33)"}],"fields":{"name":"Factory 13","price_tier":"Contract B","salesman":"KAM1","segment":"Factory","territory":"Central Java"}},
  {"id":"ACC34","score":0.49887096881866455,"rank":3,"reasons":[{"code":"reorder_due","text":"Usually orders every 62 days; last order 52 days ago"},{"code":"not_visited","text":"Not visited for 52 days (target every 31)"}],"fields":{"name":"Factory 34","price_tier":"List","salesman":"KAM1","segment":"Factory","territory":"Banten"}}],
 "plan": [
  {"date": "2026-10-01", "items": [
    {"id":"ACC01","score":0.639569878578186,"rank":1,"reasons":[{"code":"reorder_due","text":"Usually orders every 90 days; last order 134 days ago"},{"code":"high_value","text":"Top 23% of customers by revenue"},{"code":"whitespace","text":"Similar customers buy 2 products this customer has never bought"},{"code":"not_visited","text":"Not visited for 35 days (target every 31)"}],"fields":{"name":"Factory 1","price_tier":"Contract A","salesman":"KAM1","segment":"Factory","territory":"West Java"}},
    {"id":"ACC13","score":0.5112121105194092,"rank":2,"reasons":[{"code":"reorder_due","text":"Usually orders every 110 days; last order 107 days ago"},{"code":"whitespace","text":"Similar customers buy 2 products this customer has never bought"},{"code":"not_visited","text":"Not visited for 29 days (target every 33)"}],"fields":{"name":"Factory 13","price_tier":"Contract B","salesman":"KAM1","segment":"Factory","territory":"Central Java"}},
    {"id":"ACC34","score":0.49887096881866455,"rank":3,"reasons":[{"code":"reorder_due","text":"Usually orders every 62 days; last order 52 days ago"},{"code":"not_visited","text":"Not visited for 52 days (target every 31)"}],"fields":{"name":"Factory 34","price_tier":"List","salesman":"KAM1","segment":"Factory","territory":"Banten"}}]}]}
```

## 3. A pharmacy chain

**The business.** A chain of 40 pharmacy branches in the north and south of
a city orders 120 products from its own warehouse: analgesics, cough and
cold, vitamins, skin care, baby care and diabetes care, each product a brand
and its generic equivalent (the brand's subcategory is the pair, such as
"Analgesics 1"). Branches beside hospitals, in malls and in neighbourhoods
stock different mixes, and a branch carries a brand together with its
generic. They order every 7, 10 or 14 days; cough and cold doubles from
November to February. Three brands of each category are out of stock and one
was discontinued on 2026-09-01, each with its generic as the substitute.
Branches sent back most of a slow product before it expired. Four area
managers visit the branches. 12 months of history.

**The questions.** A brand is out of stock: what do we offer instead? What
should a branch stock that branches like it nearby do? Which branches are
about to order?

**Tables and columns.**

| Role | Dataset | Columns mapped |
|---|---|---|
| Orders | `orders` | customer `branch_code`, product `product_code`, date `order_date`, quantity `qty`, amount `amount` |
| Customers | `branches` | id `branch_code`, salesman `area_manager`, territory `area`, category `branch_type`, size `branch_size`, latitude `lat`, longitude `lng`, name `branch_name` |
| Products | `products` | id `product_code`, category, subcategory, brand, price, pack size, unit, end date `discontinued_on`, stock `on_hand`, substitute, name `product_name` |
| Visits | `visits` | customer `branch_code`, date `visit_date`, salesman `area_manager`, outcome |
| Salesmen | `managers` | id `manager`, visits per day, working days |
| Returns | `returns` | customer `branch_code`, product `product_code`, date `return_date`, quantity `qty` |

There are no quotes: branches order from their own warehouse.

**Train.**

| Model | Scenario | Measured | Against |
|---|---|---|---|
| `pharmacy_visits` | visits | `ordered_in_period@k` 0.775, `revenue_share@k` 1.000 | most overdue first: 0.775, 1.000; biggest first: 0.775, 1.000; most popular: 0.775, 1.000 |
| `pharmacy_products` | products | `hit_rate@10` 0.725, `ndcg@10` 0.217 | most popular: 0.525, 0.101 |
| `pharmacy_reorder` | reorder | `mae_days` 1.46, `on_time_share` 1.000 | everyone's usual gap: 2.95, 0.975 |
| `pharmacy_audience` | audience | `hit_rate@10` 0.725, `ndcg@10` 0.217 | most popular: 0.525, 0.101 |
| `pharmacy_similar` | similar | `hit_rate@10` 0.725, `ndcg@10` 0.217 | most popular: 0.525, 0.101 |

As in the industrial case, four managers can see all 40 branches in a week,
so the visit plan ties its baselines on who is visited.

**A brand out of stock.** Sehat analgesics 1 (`PH001`) has none on hand:

`POST /api/vhosts/default/ml/models/pharmacy_similar/recommend`

```json
{"for": {"product": "PH001"}, "k": 3}
```

```json
{"for": "PH001",
 "items": [
  {"id":"PH002","score":1.100000023841858,"rank":1,"reasons":[{"code":"bought_together","text":"Often bought by the same customers as PH001"},{"code":"same_kind","text":"Same subcategory (Analgesics 1)"}],"fields":{"brand":"Generik","category":"Analgesics","name":"Generik analgesics 1","pack_size":20,"subcategory":"Analgesics 1","unit":"box"}},
  {"id":"PH073","score":0.550000011920929,"rank":2,"reasons":[{"code":"bought_together","text":"Often bought by the same customers as PH001"},{"code":"same_kind","text":"Same brand (Sehat)"}],"fields":{"brand":"Sehat","category":"Skin care","name":"Sehat skin care 7","pack_size":20,"subcategory":"Skin care 7","unit":"box"}},
  {"id":"PH074","score":0.5,"rank":3,"reasons":[{"code":"bought_together","text":"Often bought by the same customers as PH001"}],"fields":{"brand":"Generik","category":"Skin care","name":"Generik skin care 7","pack_size":20,"subcategory":"Skin care 7","unit":"box"}}]}
```

Its generic comes first: the same branches buy both, and they share a
subcategory.

**What a branch should stock.** Hospital branch 1 (`BR01`), with what it has
bought listed too:

`POST /api/vhosts/default/ml/models/pharmacy_products/recommend`

```json
{"for": {"customer": "BR01"}, "k": 3, "exclude_seen": false}
```

```json
{"for": "BR01", "summary": "Hospital · small customers within 5 km buy: Sehat analgesics 7, Generik analgesics 7",
 "items": [
  {"id":"PH013","score":0.7883733510971069,"rank":1,"reasons":[{"code":"similar_customers","text":"Bought by 13 of 20 similar customers"},{"code":"peer_group","text":"Bought by 2 of 3 Hospital · small customers within 5 km"},{"code":"brand","text":"Buys a lot of Sehat (48% of spend)"}],"fields":{"brand":"Sehat","category":"Analgesics","name":"Sehat analgesics 7","pack_size":20,"subcategory":"Analgesics 7","suggested_qty":1,"suggested_text":"1 box (4 pcs)","unit":"box"}},
  {"id":"PH014","score":0.7549780607223511,"rank":2,"reasons":[{"code":"similar_customers","text":"Bought by 13 of 20 similar customers"},{"code":"peer_group","text":"Bought by 2 of 3 Hospital · small customers within 5 km"},{"code":"brand","text":"Buys a lot of Generik (27% of spend)"}],"fields":{"brand":"Generik","category":"Analgesics","name":"Generik analgesics 7","pack_size":20,"subcategory":"Analgesics 7","suggested_qty":1,"suggested_text":"1 box (4 pcs)","unit":"box"}},
  {"id":"PH008","score":0.6501975655555725,"rank":3,"reasons":[{"code":"substitute","text":"Instead of Sehat analgesics 4 (discontinued)"}],"fields":{"brand":"Generik","category":"Analgesics","name":"Generik analgesics 4","pack_size":20,"subcategory":"Analgesics 4","unit":"box"}}]}
```

Sehat analgesics 4 was discontinued on 2026-09-01: where it would have ranked,
its generic takes its place. A brand out of stock is replaced the same way:

`POST /api/vhosts/default/ml/models/pharmacy_products/recommend`

```json
{"for": {"customer": "BR05"}, "k": 3, "exclude_seen": false}
```

```json
{"for": "BR05",
 "items": [
  {"id":"PH006","score":0.7540931105613708,"rank":1,"reasons":[{"code":"substitute","text":"Instead of Medika analgesics 3 (out of stock)"}],"fields":{"brand":"Generik","category":"Analgesics","name":"Generik analgesics 3","pack_size":10,"subcategory":"Analgesics 3","suggested_qty":1,"suggested_text":"1 box (9 pcs)","unit":"box"}},
  {"id":"PH037","score":0.7182651162147522,"rank":2,"reasons":[{"code":"similar_customers","text":"Bought by 8 of 20 similar customers"},{"code":"brand","text":"Buys a lot of Medika (42% of spend)"}],"fields":{"brand":"Medika","category":"Cough and cold","name":"Medika cough and cold 9","pack_size":10,"subcategory":"Cough and cold 9","suggested_qty":1,"suggested_text":"1 box (10 pcs)","unit":"box"}},
  {"id":"PH038","score":0.6885541677474976,"rank":3,"reasons":[{"code":"similar_customers","text":"Bought by 8 of 20 similar customers"},{"code":"brand","text":"Buys a lot of Generik (25% of spend)"}],"fields":{"brand":"Generik","category":"Cough and cold","name":"Generik cough and cold 9","pack_size":10,"subcategory":"Cough and cold 9","suggested_qty":2,"suggested_text":"2 boxs (12 pcs)","unit":"box"}}]}
```

A call can give today's stock in `stock` (`{"PH042": 0}`), replacing the
stock the model was trained with for the products it names.

**A returned product.** Mall branch 18 (`BR18`) sent back 41 of the
58 Generik baby 10 (`PH100`) it had bought, near expiry. Without the returns
role:

`POST /api/vhosts/default/ml/models/pharmacy_products_plain/recommend`

```json
{"for": {"customer": "BR18"}, "k": 3, "exclude_seen": false}
```

```json
{"for": "BR18",
 "items": [
  {"id":"PH099","score":0.735736608505249,"rank":1,"reasons":[{"code":"similar_customers","text":"Bought by 8 of 20 similar customers"},{"code":"brand","text":"Buys a lot of Sehat (45% of spend)"}],"fields":{"brand":"Sehat","category":"Baby","name":"Sehat baby 10","pack_size":20,"subcategory":"Baby 10","suggested_qty":1,"suggested_text":"1 box (8 pcs)","unit":"box"}},
  {"id":"PH100","score":0.6993699669837952,"rank":2,"reasons":[{"code":"similar_customers","text":"Bought by 8 of 20 similar customers"},{"code":"brand","text":"Buys a lot of Generik (25% of spend)"}],"fields":{"brand":"Generik","category":"Baby","name":"Generik baby 10","pack_size":20,"subcategory":"Baby 10","suggested_qty":1,"suggested_text":"1 box (7 pcs)","unit":"box"}},
  {"id":"PH093","score":0.6712750196456909,"rank":3,"reasons":[{"code":"similar_customers","text":"Bought by 6 of 20 similar customers"},{"code":"brand","text":"Buys a lot of Sehat (45% of spend)"}],"fields":{"brand":"Sehat","category":"Baby","name":"Sehat baby 7","pack_size":20,"subcategory":"Baby 7","suggested_qty":1,"suggested_text":"1 box (8 pcs)","unit":"box"}}]}
```

With it:

`POST /api/vhosts/default/ml/models/pharmacy_products/recommend`

```json
{"for": {"customer": "BR18"}, "k": 3, "exclude_seen": false}
```

```json
{"for": "BR18",
 "items": [
  {"id":"PH099","score":0.735736608505249,"rank":1,"reasons":[{"code":"similar_customers","text":"Bought by 8 of 20 similar customers"},{"code":"brand","text":"Buys a lot of Sehat (45% of spend)"}],"fields":{"brand":"Sehat","category":"Baby","name":"Sehat baby 10","pack_size":20,"subcategory":"Baby 10","suggested_qty":1,"suggested_text":"1 box (8 pcs)","unit":"box"}},
  {"id":"PH093","score":0.6712750196456909,"rank":2,"reasons":[{"code":"similar_customers","text":"Bought by 6 of 20 similar customers"},{"code":"brand","text":"Buys a lot of Sehat (45% of spend)"}],"fields":{"brand":"Sehat","category":"Baby","name":"Sehat baby 7","pack_size":20,"subcategory":"Baby 7","suggested_qty":1,"suggested_text":"1 box (8 pcs)","unit":"box"}},
  {"id":"PH019","score":0.6686062216758728,"rank":3,"reasons":[{"code":"similar_customers","text":"Bought by 6 of 20 similar customers"},{"code":"brand","text":"Buys a lot of Sehat (45% of spend)"}],"fields":{"brand":"Sehat","category":"Analgesics","name":"Sehat analgesics 10","pack_size":20,"subcategory":"Analgesics 10","suggested_qty":1,"suggested_text":"1 box (7 pcs)","unit":"box"}}]}
```

**Which branches order next.** AM1's branches due within two days:

`POST /api/vhosts/default/ml/models/pharmacy_reorder/recommend`

```json
{"for": {"salesman": "AM1"}, "within_days": 2, "k": 2}
```

```json
{"for": "AM1",
 "items": [
  {"id":"BR01","score":1,"rank":1,"reasons":[{"code":"reorder_due","text":"Usually orders every 7 days; last order 8 days ago"}],"fields":{"lat":-6.132160186767578,"lon":106.86544799804688,"name":"Hospital branch 1","salesman":"AM1","segment":"Hospital","size":"small","territory":"North"},"due_date":"2026-09-30","overdue":true,"products":[{"id":"PH042","due_date":"2026-09-16","overdue":true,"fields":{"brand":"Generik","category":"Vitamins","name":"Generik vitamins 1","pack_size":20,"subcategory":"Vitamins 1","suggested_qty":1,"suggested_text":"1 box (4 pcs)","unit":"box"}},{"id":"PH039","due_date":"2026-09-22","overdue":true,"fields":{"brand":"Sehat","category":"Cough and cold","name":"Sehat cough and cold 10","pack_size":20,"subcategory":"Cough and cold 10","suggested_qty":1,"suggested_text":"1 box (5 pcs)","unit":"box"}},{"id":"PH006","due_date":"2026-09-23","overdue":true,"fields":{"brand":"Generik","category":"Analgesics","instead_of":"PH005","name":"Generik analgesics 3","pack_size":10,"subcategory":"Analgesics 3","suggested_qty":1,"suggested_text":"1 box (4 pcs)","unit":"box"}},{"id":"PH026","due_date":"2026-09-23","overdue":true,"fields":{"brand":"Generik","category":"Cough and cold","name":"Generik cough and cold 3","pack_size":10,"subcategory":"Cough and cold 3","suggested_qty":1,"suggested_text":"1 box (6 pcs)","unit":"box"}},{"id":"PH106","due_date":"2026-09-23","overdue":true,"fields":{"brand":"Generik","category":"Diabetes care","instead_of":"PH105","name":"Generik diabetes care 3","pack_size":10,"subcategory":"Diabetes care 3","suggested_qty":1,"suggested_text":"1 box (4 pcs)","unit":"box"}},{"id":"PH048","due_date":"2026-09-25","overdue":true,"fields":{"brand":"Generik","category":"Vitamins","name":"Generik vitamins 4","pack_size":20,"subcategory":"Vitamins 4","suggested_qty":1,"suggested_text":"1 box (3 pcs)","unit":"box"}},{"id":"PH022","due_date":"2026-09-26","overdue":true,"fields":{"brand":"Generik","category":"Cough and cold","name":"Generik cough and cold 1","pack_size":20,"subcategory":"Cough and cold 1","suggested_qty":1,"suggested_text":"1 box (5 pcs)","unit":"box"}},{"id":"PH108","due_date":"2026-09-27","overdue":true,"fields":{"brand":"Generik","category":"Diabetes care","name":"Generik diabetes care 4","pack_size":20,"subcategory":"Diabetes care 4","suggested_qty":1,"suggested_text":"1 box (4 pcs)","unit":"box"}},{"id":"PH046","due_date":"2026-09-29","overdue":true,"fields":{"brand":"Generik","category":"Vitamins","instead_of":"PH045","name":"Generik vitamins 3","pack_size":10,"subcategory":"Vitamins 3","suggested_qty":1,"suggested_text":"1 box (4 pcs)","unit":"box"}},{"id":"PH104","due_date":"2026-10-01","fields":{"brand":"Generik","category":"Diabetes care","name":"Generik diabetes care 2","pack_size":30,"subcategory":"Diabetes care 2","suggested_qty":1,"suggested_text":"1 box (4 pcs)","unit":"box"}}]},
  {"id":"BR13","score":1,"rank":2,"reasons":[{"code":"reorder_due","text":"Usually orders every 7 days; last order 6 days ago"}],"fields":{"lat":-6.130280017852783,"lon":106.85984802246094,"name":"Hospital branch 13","salesman":"AM1","segment":"Hospital","size":"small","territory":"North"},"due_date":"2026-10-02","products":[{"id":"PH109","due_date":"2026-09-12","overdue":true,"fields":{"brand":"Prima","category":"Diabetes care","name":"Prima diabetes care 5","pack_size":30,"subcategory":"Diabetes care 5","suggested_qty":1,"suggested_text":"1 box (4 pcs)","unit":"box"}},{"id":"PH114","due_date":"2026-09-20","overdue":true,"fields":{"brand":"Generik","category":"Diabetes care","name":"Generik diabetes care 7","pack_size":20,"subcategory":"Diabetes care 7","suggested_qty":1,"suggested_text":"1 box (3 pcs)","unit":"box"}},{"id":"PH008","due_date":"2026-09-21","overdue":true,"fields":{"brand":"Generik","category":"Analgesics","name":"Generik analgesics 4","pack_size":20,"subcategory":"Analgesics 4","suggested_qty":1,"suggested_text":"1 box (4 pcs)","unit":"box"}},{"id":"PH059","due_date":"2026-09-22","overdue":true,"fields":{"brand":"Sehat","category":"Vitamins","name":"Sehat vitamins 10","pack_size":20,"subcategory":"Vitamins 10","suggested_qty":1,"suggested_text":"1 box (3 pcs)","unit":"box"}},{"id":"PH051","due_date":"2026-09-25","overdue":true,"fields":{"brand":"Medika","category":"Vitamins","name":"Medika vitamins 6","pack_size":10,"subcategory":"Vitamins 6","suggested_qty":1,"suggested_text":"1 box (5 pcs)","unit":"box"}},{"id":"PH106","due_date":"2026-09-26","overdue":true,"fields":{"brand":"Generik","category":"Diabetes care","instead_of":"PH105","name":"Generik diabetes care 3","pack_size":10,"subcategory":"Diabetes care 3","suggested_qty":1,"suggested_text":"1 box (4 pcs)","unit":"box"}},{"id":"PH113","due_date":"2026-09-26","overdue":true,"fields":{"brand":"Sehat","category":"Diabetes care","name":"Sehat diabetes care 7","pack_size":20,"subcategory":"Diabetes care 7","suggested_qty":1,"suggested_text":"1 box (4 pcs)","unit":"box"}},{"id":"PH042","due_date":"2026-09-28","overdue":true,"fields":{"brand":"Generik","category":"Vitamins","name":"Generik vitamins 1","pack_size":20,"subcategory":"Vitamins 1","suggested_qty":1,"suggested_text":"1 box (4 pcs)","unit":"box"}},{"id":"PH004","due_date":"2026-09-30","overdue":true,"fields":{"brand":"Generik","category":"Analgesics","instead_of":"PH003","name":"Generik analgesics 2","pack_size":30,"subcategory":"Analgesics 2","suggested_qty":1,"suggested_text":"1 box (4 pcs)","unit":"box"}},{"id":"PH060","due_date":"2026-09-30","overdue":true,"fields":{"brand":"Generik","category":"Vitamins","name":"Generik vitamins 10","pack_size":20,"subcategory":"Vitamins 10","suggested_qty":1,"suggested_text":"1 box (5 pcs)","unit":"box"}}]}]}
```

**Who stocks a product.** The hospital branches most likely to want Sehat
analgesics 7 (`PH013`):

`POST /api/vhosts/default/ml/models/pharmacy_audience/recommend`

```json
{"for": {"product": "PH013", "segment": "Hospital"}, "k": 3}
```

```json
{"for": "PH013",
 "items": [
  {"id":"BR21","score":0.6985380053520203,"rank":1,"reasons":[{"code":"similar_customers","text":"13 of 20 similar customers buy it"}],"fields":{"lat":-6.156779766082764,"lon":106.87741088867188,"name":"Hospital branch 21","salesman":"AM1","segment":"Hospital","size":"large","territory":"North"}},
  {"id":"BR13","score":0.5587482452392578,"rank":2,"reasons":[{"code":"similar_customers","text":"8 of 20 similar customers buy it"},{"code":"peer_group","text":"Bought by 3 of 3 Hospital · small customers within 5 km"}],"fields":{"lat":-6.130280017852783,"lon":106.85984802246094,"name":"Hospital branch 13","salesman":"AM1","segment":"Hospital","size":"small","territory":"North"}},
  {"id":"BR17","score":0.29972633719444275,"rank":3,"reasons":[{"code":"similar_customers","text":"8 of 20 similar customers buy it"}],"fields":{"lat":-6.147349834442139,"lon":106.86949157714844,"name":"Hospital branch 17","salesman":"AM1","segment":"Hospital","size":"medium","territory":"North"}}]}
```

**An area manager's week.**

`POST /api/vhosts/default/ml/models/pharmacy_visits/recommend`

```json
{"for": {"salesman": "AM1"}, "k": 3}
```

```json
{"for": "AM1",
 "items": [
  {"id":"BR29","score":0.7400000095367432,"rank":1,"reasons":[{"code":"reorder_due","text":"Usually orders every 10 days; last order 5 days ago"},{"code":"high_value","text":"Top 25% of customers by revenue"},{"code":"not_visited","text":"Not visited for 45 days (target every 22)"},{"code":"whitespace","text":"Similar customers buy 22 products this customer has never bought"}],"fields":{"lat":-6.138390064239502,"lon":106.85427856445312,"name":"Hospital branch 29","salesman":"AM1","segment":"Hospital","size":"medium","territory":"North"}},
  {"id":"BR21","score":0.6675000190734863,"rank":2,"reasons":[{"code":"reorder_due","text":"Usually orders every 14 days; last order 11 days ago"},{"code":"not_visited","text":"Not visited for 43 days (target every 22)"}],"fields":{"lat":-6.156779766082764,"lon":106.87741088867188,"name":"Hospital branch 21","salesman":"AM1","segment":"Hospital","size":"large","territory":"North"}},
  {"id":"BR17","score":0.6106818318367004,"rank":3,"reasons":[{"code":"reorder_due","text":"Usually orders every 10 days; last order 7 days ago"},{"code":"not_visited","text":"Not visited for 37 days (target every 22)"}],"fields":{"lat":-6.147349834442139,"lon":106.86949157714844,"name":"Hospital branch 17","salesman":"AM1","segment":"Hospital","size":"medium","territory":"North"}}],
 "plan": [
  {"date": "2026-10-01", "items": [
    {"id":"BR29","score":0.7400000095367432,"rank":1,"reasons":[{"code":"reorder_due","text":"Usually orders every 10 days; last order 5 days ago"},{"code":"high_value","text":"Top 25% of customers by revenue"},{"code":"not_visited","text":"Not visited for 45 days (target every 22)"},{"code":"whitespace","text":"Similar customers buy 22 products this customer has never bought"}],"fields":{"lat":-6.138390064239502,"lon":106.85427856445312,"name":"Hospital branch 29","salesman":"AM1","segment":"Hospital","size":"medium","territory":"North"}},
    {"id":"BR17","score":0.6106818318367004,"rank":3,"reasons":[{"code":"reorder_due","text":"Usually orders every 10 days; last order 7 days ago"},{"code":"not_visited","text":"Not visited for 37 days (target every 22)"}],"fields":{"lat":-6.147349834442139,"lon":106.86949157714844,"name":"Hospital branch 17","salesman":"AM1","segment":"Hospital","size":"medium","territory":"North"}},
    {"id":"BR21","score":0.6675000190734863,"rank":2,"reasons":[{"code":"reorder_due","text":"Usually orders every 14 days; last order 11 days ago"},{"code":"not_visited","text":"Not visited for 43 days (target every 22)"}],"fields":{"lat":-6.156779766082764,"lon":106.87741088867188,"name":"Hospital branch 21","salesman":"AM1","segment":"Hospital","size":"large","territory":"North"}}]}]}
```

## What each signal changed

The same tables, trained with and without a signal, measured on the same
held-back period (`ndcg@10` and `hit_rate@10` of a customer's products):

| Case | Trained with | `hit_rate@10` | `ndcg@10` | Evaluation |
|---|---|---|---|---|
| Distributor | orders only | 0.119 | 0.045 | |
| Distributor | + customers (no coordinates), products, visits, salesmen | 0.140 | 0.040 | `peer_groups` 27, `nearby_customers` 0 |
| Distributor | + coordinates | 0.140 | 0.045 | `nearby_customers` 110 |
| Distributor | + returns and quotes | 0.140 | 0.045 | `returns` 25 |
| Industrial | + returns and quotes, peers of all five customer attributes | 0.717 | 0.405 | `peer_groups` 39 |
| Industrial | industry and price tier, no returns or quotes | 0.717 | 0.407 | `peer_groups` 9 |
| Industrial | + returns and quotes | 0.717 | 0.414 | `returns` 6 |
| Pharmacy | without returns | 0.725 | 0.217 | |
| Pharmacy | + returns | 0.725 | 0.217 | `returns` 8 |

- **The customers and products tables.** In the distributor, the
  customers' type, size, channel and city (peers) and the products' details
  together lift `hit_rate@10` from 0.119 to
  0.140: more outlets find at least one product they then
  bought. `ndcg@10` barely moves, because the products an outlet buys most
  were already at the top.
- **Coordinates (nearby peers).** 110
  of the 143 tested outlets had at least 3
  peers within 5 km, who share their neighbourhood's favourite products (the
  generator gives each neighbourhood its own): `ndcg@10`
  0.040 with whole-city groups, below the
  0.045 of orders alone, and 0.045 with
  nearby peers. An outlet with fewer nearby peers (such
  as Restaurant Surabaya 59 below) uses its whole group, and its reasons say
  so without "within 5 km".
- **Price tier (industrial).** Contract A accounts are offered only premium
  lines and list-price accounts only economy lines: the peers that count are
  of the same tier, and `test_industrial_contract_tiers_are_offered_their_own_brand_line`
  checks the first five offers of every such account. Too many attributes make groups too small, as the
  table shows.
- **Returns.** A product a customer sent back at least half of is gone from
  its offers, and nothing else changes. Restaurant Surabaya 59 (`OUT269`) sent
  back 130 of the 217 Pelita Dates 6 (`FM081`) it bought. Without the returns
  role:

  `POST /api/vhosts/default/ml/models/fmcg_products_plain/recommend`

  ```json
  {"for": {"customer": "OUT269"}, "k": 3, "exclude_seen": false}
  ```

  ```json
  {"for": "OUT269",
   "items": [
    {"id":"FM080","score":0.7171053290367126,"rank":1,"reasons":[{"code":"similar_customers","text":"Bought by 20 of 20 similar customers"},{"code":"peer_group","text":"Bought by 4 of 5 Restaurant · medium · HoReCa · Surabaya customers"}],"fields":{"brand":"Nusantara","category":"Cooking","name":"Nusantara Cooking oil 5","pack_size":24,"subcategory":"Cooking oil","suggested_qty":1,"suggested_text":"1 box (16 pcs)","unit":"box"}},
    {"id":"FM081","score":0.7066337466239929,"rank":2,"reasons":[{"code":"similar_customers","text":"Bought by 18 of 20 similar customers"},{"code":"peer_group","text":"Bought by 4 of 5 Restaurant · medium · HoReCa · Surabaya customers"}],"fields":{"brand":"Pelita","category":"Cooking","name":"Pelita Dates 6","pack_size":40,"subcategory":"Dates","suggested_qty":1,"suggested_text":"1 carton (5 pcs)","unit":"carton"}},
    {"id":"FM077","score":0.7035707235336304,"rank":3,"reasons":[{"code":"similar_customers","text":"Bought by 19 of 20 similar customers"},{"code":"peer_group","text":"Bought by 4 of 5 Restaurant · medium · HoReCa · Surabaya customers"},{"code":"brand","text":"Buys a lot of Sedap Rasa (28% of spend)"}],"fields":{"brand":"Sedap Rasa","category":"Cooking","name":"Sedap Rasa Dates 2","pack_size":24,"subcategory":"Dates","suggested_qty":1,"suggested_text":"1 box (17 pcs)","unit":"box"}}]}
  ```

  With it, the dates are gone and the rest moves up:

  `POST /api/vhosts/default/ml/models/fmcg_products/recommend`

  ```json
  {"for": {"customer": "OUT269"}, "k": 3, "exclude_seen": false}
  ```

  ```json
  {"for": "OUT269",
   "items": [
    {"id":"FM080","score":0.7171053290367126,"rank":1,"reasons":[{"code":"similar_customers","text":"Bought by 20 of 20 similar customers"},{"code":"peer_group","text":"Bought by 4 of 5 Restaurant · medium · HoReCa · Surabaya customers"}],"fields":{"brand":"Nusantara","category":"Cooking","name":"Nusantara Cooking oil 5","pack_size":24,"subcategory":"Cooking oil","suggested_qty":1,"suggested_text":"1 box (16 pcs)","unit":"box"}},
    {"id":"FM077","score":0.7035707235336304,"rank":2,"reasons":[{"code":"similar_customers","text":"Bought by 19 of 20 similar customers"},{"code":"peer_group","text":"Bought by 4 of 5 Restaurant · medium · HoReCa · Surabaya customers"},{"code":"brand","text":"Buys a lot of Sedap Rasa (28% of spend)"}],"fields":{"brand":"Sedap Rasa","category":"Cooking","name":"Sedap Rasa Dates 2","pack_size":24,"subcategory":"Dates","suggested_qty":1,"suggested_text":"1 box (17 pcs)","unit":"box"}},
    {"id":"FM086","score":0.7028807997703552,"rank":3,"reasons":[{"code":"similar_customers","text":"Bought by 18 of 20 similar customers"},{"code":"peer_group","text":"Bought by 5 of 5 Restaurant · medium · HoReCa · Surabaya customers"}],"fields":{"brand":"Nusantara","category":"Cooking","name":"Nusantara Cooking oil 11","pack_size":24,"subcategory":"Cooking oil","suggested_qty":1,"suggested_text":"1 box (11 pcs)","unit":"box"}}]}
  ```

  Returns and quotes move the metrics little: the distributor's and the
  pharmacy's are unchanged, the industrial supplier's `ndcg@10` goes from
  0.407 to 0.414.
- **Lost quotes.** A product whose quote a customer lost in the last 90
  days is gone from its offers and the customer from its audience. Pharmacy
  Jakarta 3 (`OUT003`) lost a quote for Kapal Api Jaya Pain relief 1
  (`FM106`) on 2026-09-01 (price). Without the quotes role it was first:

  `POST /api/vhosts/default/ml/models/fmcg_products_plain/recommend`

  ```json
  {"for": {"customer": "OUT003"}, "k": 3}
  ```

  ```json
  {"for": "OUT003", "summary": "Pharmacy · small · Pharmacy · Jakarta customers within 5 km buy: Pelita Baby soap 8",
   "items": [
    {"id":"FM106","score":0.5445833802223206,"rank":1,"reasons":[{"code":"similar_customers","text":"Bought by 9 of 20 similar customers"},{"code":"brand","text":"Buys a lot of Kapal Api Jaya (29% of spend)"}],"fields":{"brand":"Kapal Api Jaya","category":"OTC medicine","name":"Kapal Api Jaya Pain relief 1","pack_size":12,"subcategory":"Pain relief","unit":"carton"}},
    {"id":"FM143","score":0.5039825439453125,"rank":2,"reasons":[{"code":"similar_customers","text":"Bought by 5 of 20 similar customers"},{"code":"peer_group","text":"Bought by 1 of 4 Pharmacy · small · Pharmacy · Jakarta customers within 5 km"},{"code":"brand","text":"Buys a lot of Pelita (40% of spend)"}],"fields":{"brand":"Pelita","category":"Baby care","name":"Pelita Baby soap 8","pack_size":24,"subcategory":"Baby soap","unit":"box"}},
    {"id":"FM126","score":0.468992680311203,"rank":3,"reasons":[{"code":"similar_customers","text":"Bought by 6 of 20 similar customers"},{"code":"brand","text":"Buys a lot of Kapal Api Jaya (29% of spend)"}],"fields":{"brand":"Kapal Api Jaya","category":"Vitamins","name":"Kapal Api Jaya Multivitamin 6","pack_size":40,"subcategory":"Multivitamin","unit":"carton"}}]}
  ```

  With it:

  `POST /api/vhosts/default/ml/models/fmcg_products/recommend`

  ```json
  {"for": {"customer": "OUT003"}, "k": 3}
  ```

  ```json
  {"for": "OUT003", "summary": "Pharmacy · small · Pharmacy · Jakarta customers within 5 km buy: Pelita Baby soap 8, Samudra Pain relief 11",
   "items": [
    {"id":"FM143","score":0.5039825439453125,"rank":1,"reasons":[{"code":"similar_customers","text":"Bought by 5 of 20 similar customers"},{"code":"peer_group","text":"Bought by 1 of 4 Pharmacy · small · Pharmacy · Jakarta customers within 5 km"},{"code":"brand","text":"Buys a lot of Pelita (40% of spend)"}],"fields":{"brand":"Pelita","category":"Baby care","name":"Pelita Baby soap 8","pack_size":24,"subcategory":"Baby soap","unit":"box"}},
    {"id":"FM126","score":0.468992680311203,"rank":2,"reasons":[{"code":"similar_customers","text":"Bought by 6 of 20 similar customers"},{"code":"brand","text":"Buys a lot of Kapal Api Jaya (29% of spend)"}],"fields":{"brand":"Kapal Api Jaya","category":"Vitamins","name":"Kapal Api Jaya Multivitamin 6","pack_size":40,"subcategory":"Multivitamin","unit":"carton"}},
    {"id":"FM116","score":0.46078240871429443,"rank":3,"reasons":[{"code":"similar_customers","text":"Bought by 6 of 20 similar customers"},{"code":"peer_group","text":"Bought by 1 of 4 Pharmacy · small · Pharmacy · Jakarta customers within 5 km"}],"fields":{"brand":"Samudra","category":"OTC medicine","name":"Samudra Pain relief 11","pack_size":24,"subcategory":"Pain relief","unit":"box"}}]}
  ```

- **Open quotes.** A quote of the last 90 days neither lost nor ordered
  since adds 0.1 and the reason `quoted`, so the salesman follows it up:
  `IN002` for `ACC04` and `FM015` for `OUT207` above.
- **Won quotes** are not purchases: a won quote counts once it is ordered,
  through the orders. Peers, the collaborative filter and every other signal
  read only the orders.
- **End of life and stock.** Never in a list of products or an audience,
  with the substitute in their place where one is set and available
  (`test_what_cannot_be_sold_is_never_recommended`).
- **The Ramadan spike** changes how much the outlets bought in February
  and March, not how often they order. The reorder model reads each outlet's
  usual gap between orders, and a suggested quantity is the outlet's median
  quantity per order, so six busy weeks do not move October's due dates:
  `mae_days` 1.93, `on_time_share` 0.993, against everyone's usual gap: 6.72, 0.593.
