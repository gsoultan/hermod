# Preparing data for sales recommendations

This guide is for the people who own the sales data: sales managers, ERP
administrators, analysts. It explains what Hermod needs to suggest which
customers to visit, which products to offer, when customers will reorder,
who to offer a product to, and which products are alike. No machine learning
knowledge is needed. The technical reference is "Recommendation scenarios" in
[ml.md](ml.md).

## What Hermod can answer

| Question | Hermod calls it | What you get |
|---|---|---|
| Which customers should this salesman visit next week? | Visits | A day-by-day visit list, most urgent first, each with the reasons. |
| What should we offer this customer? | Products | A short list of products, with why each one fits. |
| When will this customer order again? | Reorder | The day they are due to order, and whether it has passed. |
| Which customers will buy in the next 2 weeks, or month? | Reorder | Everyone (or a salesman's customers, or a product's buyers) due in that period, overdue ones marked, each with the products they are due for. |
| Who should we offer this product to? | Audience | The customers most likely to want it. |
| What is like this product? | Similar | Products bought by the same kind of customers. |

Every answer comes with reasons in plain words, such as *"Usually orders every
30 days; last order 41 days ago"*, so a salesman can see why a customer is on
the list.

For "who will buy in the next 2 weeks", Reorder needs the **Product** column
too: the date a customer will buy a product again comes from how often they
bought that product before, so only products a customer bought on at least
two different days get a date. A reorder model trained without the product
column still lists the customers, just without their products.

## The data, table by table

You give Hermod up to five tables. Each is loaded as a **dataset** (usually
straight from your database), and you tell Hermod which column means what.
Only the first is required.

### 1. Sales (required)

One row per order line or invoice line.

| Column | Required | Example |
|---|---|---|
| Customer | Yes | `C-1042` |
| Date of the sale | Yes | `2026-09-14` |
| Product | Yes, except for the visit plan | `SKU-881` |
| Quantity | No | `12` |
| Amount (money) | No, but strongly recommended | `1 450 000` |

With amounts, "valuable customer" means money, not just number of orders.
Returns should be left out or kept as their own rows with negative
quantities (Hermod flags them; see below).

### 2. Customers

| Column | Needed for | Example |
|---|---|---|
| Customer id (the same ids as in Sales) | Everything below | `C-1042` |
| Salesman | **The visit plan** (required there) | `rep7` |
| Territory or region | Keeping plans to a territory | `North` |
| Segment or tier | Rules such as "only key accounts" | `Gold` |
| Latitude and longitude | Ordering each day's visits by distance | `-6.2088`, `106.8456` |
| Blocked / credit hold | Never suggesting a blocked customer | `true` |
| Name | Showing a name instead of an id | `Toko Maju` |

### 3. Products

| Column | Needed for | Example |
|---|---|---|
| Product id (the same ids as in Sales) | Everything below | `SKU-881` |
| Category | Rules such as "at most 3 per category" | `Beverages` |
| Price, margin | Favouring profitable products | `25 000`, `0.22` |
| Active | Never suggesting a discontinued product | `true` |
| Name | Showing a name | `Jasmine tea 500 ml` |

### 4. Visits (optional, for the visit plan)

One row per visit a salesman made: customer, date, and optionally the
salesman and the outcome. With it, Hermod knows who was seen recently and does
not send a salesman back too soon.

### 5. Salesmen (optional, for the visit plan)

One row per salesman: how many visits a day they can make, and which days
they work (`mon,tue,wed,thu,fri`). Without it, Hermod plans 8 visits a day,
Monday to Friday.

## How much history is enough

| For | At least | Better |
|---|---|---|
| Visit plan | 2 weeks of sales, 500 customers, 2 orders each | 6 to 12 months |
| Reorder | 6 months of sales, 500 customers, 2 orders each | 12 months or more |
| Products, audience, similar | 1 year of sales, 1000 customers, 3 orders each | 2 years |

Hermod still trains with less, but warns that the answers will be weaker.

## The data health check

Before training, Hermod's data health check reads your tables and reports
each problem as green (fine), amber (works, but answers will be weaker) or red
(must be fixed first), with a suggested fix:

| Check | What it means | Usual fix |
|---|---|---|
| Few customers | Not enough customers to learn patterns. | Include more branches or a longer period. |
| Few transactions per customer | Most customers bought only once or twice. | Load a longer history. |
| Short history | Not enough months of sales. | Load older orders. |
| Too many products | 50 000 products or more. | Leave out inactive products, or use a product group. |
| Duplicates | The same line appears more than once. | Remove duplicate rows in the query. |
| Future dates | Sales dated after today. | Fix the dates, or leave out planned orders. |
| Negative quantities | Returns or credit notes mixed with sales. | Leave them out of the query. |
| Unmatched ids | Customers or products in Sales that are missing from their table (or the reverse). | Make sure both use the same id, with the same spelling and leading zeros. |
| Missing role | A table or column the chosen question needs is not set. | Map it, for example the Salesman column for a visit plan. |
| Bad column type | A date or number column holds text. | Convert the column in the query. |

## Rules you can set

Rules turn company policy into the answers. They are set when training and
can be narrowed (never widened) for a single request:

- **Only** a salesman's own customers, a territory, or a segment.
- **Leave out** customers or products bought from (or visited) in the last N
  days.
- **Favour** high-margin products, or a list of products on promotion.
- **At most** N results per category or per territory.
- **Visits per day** and working days for a salesman.
- **Always include** or **never include** particular customers or products.

## Keeping it up to date

- Set each dataset to **refresh on a schedule** (for example every night at
  2:00), so it always holds the latest sales.
- Set the model to **retrain** on a schedule, or when its accuracy drops.
- Optionally set it to **build its answers** for every salesman, customer or
  product into a table of your own each morning, so a CRM or report can show
  the day's plan without calling Hermod.
- Report back what happened (shown, accepted, dismissed, visited, ordered).
  The model page then shows how often suggestions are taken up.

## A checklist before you start

1. Sales lines with customer, product, date, quantity and amount, for at
   least a year where possible.
2. A customer table using the same customer ids, with salesman (for visit
   plans), territory and, if you have them, coordinates.
3. A product table using the same product ids, with category, margin and
   an active flag.
4. Returns, cancelled and test orders left out.
5. Run the data health check and clear every red item.
