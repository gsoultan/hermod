# Instructions

- Following Playwright test failed.
- Explain why, be concise, respect Playwright best practices.
- Provide a snippet of code with the fix, if possible.

# Test info

- Name: rabbitmq_e2e.spec.ts >> RabbitMQ to Postgres E2E and UI Logic >> RabbitMQ -> Transformations -> Postgres Sink (Scenario)
- Location: ui/__tests__/rabbitmq_e2e.spec.ts:24:3

# Error details

```
Error: expect(locator).toContainText(expected) failed

Locator: locator('div[role="dialog"]')
Expected substring: "Configure Transformation"
Timeout: 20000ms
Error: element(s) not found

Call log:
  - Expect "toContainText" with timeout 20000ms
  - waiting for locator('div[role="dialog"]')

```

```yaml
- banner:
  - img
  - paragraph: Hermod
  - button "Search... Ctrl+K":
    - img
    - text: Search... Ctrl+K
  - img
  - combobox "Select VHost": All VHosts
  - img
  - button "Toggle color scheme":
    - img
  - button "A"
- navigation:
  - paragraph: Main Menu
  - link "Dashboard":
    - /url: /
    - img
    - text: Dashboard
    - img
  - link "Sources":
    - /url: /sources
    - img
    - text: Sources
  - link "Sinks":
    - /url: /sinks
    - img
    - text: Sinks
  - link "Workflows":
    - /url: /workflows
    - img
    - text: Workflows
  - link "Approvals":
    - /url: /approvals
    - img
    - text: Approvals
  - link "Logs":
    - /url: /logs
    - img
    - text: Logs
  - link "Schema Registry":
    - /url: /schemas
    - img
    - text: Schema Registry
  - link "Data Lineage":
    - /url: /lineage
    - img
    - text: Data Lineage
  - link "Marketplace":
    - /url: /marketplace
    - img
    - text: Marketplace
  - link "Mesh Health":
    - /url: /health
    - img
    - text: Mesh Health
  - paragraph: Administration
  - link "Virtual Hosts":
    - /url: /vhosts
    - img
    - text: Virtual Hosts
  - link "Workers":
    - /url: /workers
    - img
    - text: Workers
  - link "Users":
    - /url: /users
    - img
    - text: Users
  - paragraph: System
  - link "Settings":
    - /url: /settings
    - img
    - text: Settings
  - link "Audit Logs":
    - /url: /audit-logs
    - img
    - text: Audit Logs
  - button "Collapse Sidebar":
    - img
    - text: Collapse Sidebar
  - paragraph: Hermod dev (Enterprise Edition)
- main:
  - link "Back":
    - /url: /workflows
    - img
    - text: Back
  - separator
  - heading "RMQ Flow 1784889315832" [level=3]
  - text: Inactive
  - paragraph: rmq_vhost_1784889315832
  - link "Edit Workflow":
    - /url: /workflows/0c34580f-d1fc-460f-92ac-a4d6f0c69cef/edit
    - img
    - text: Edit Workflow
  - button "Export JSON":
    - img
    - text: Export JSON
  - tablist:
    - tab "Graph View" [selected]:
      - img
      - text: Graph View
    - tab "Message Traces":
      - img
      - text: Message Traces
    - tab "History":
      - img
      - text: History
    - tab "Logs":
      - img
      - text: Logs
    - tab "Debugger":
      - img
      - text: Debugger
    - tab "Optimization":
      - img
      - text: Optimization
  - tabpanel "Graph View":
    - application:
      - img:
        - group "Edge from src1 to t1_map"
      - img:
        - group "Edge from t1_map to r1_router"
      - img:
        - group "Edge from r1_router to t2_filter"
      - img:
        - group "Edge from t2_filter to db1"
      - img:
        - group "Edge from db1 to t3_fields"
      - img:
        - group "Edge from r1_router to t3_fields"
      - img:
        - group "Edge from t3_fields to snk1"
      - group:
        - img
        - paragraph: TRIGGER
        - paragraph: src1
      - group:
        - img
        - paragraph: TRANSFORM
        - text: map
        - paragraph: Rename Msg
      - group:
        - img
        - paragraph: TRANSFORM
        - text: map
        - paragraph: Priority Switch
      - group:
        - img
        - paragraph: TRANSFORM
        - text: map
        - paragraph: If High Priority
      - group:
        - img
        - paragraph: TRANSFORM
        - text: map
        - paragraph: User Info Lookup
      - group:
        - img
        - paragraph: TRANSFORM
        - text: map
        - paragraph: Fields Projection
      - group:
        - img
        - paragraph: SINK
        - paragraph: snk1
      - img
      - button "Zoom In":
        - img
      - button "Zoom Out":
        - img
      - button "Fit View":
        - img
      - button "Toggle Interactivity":
        - img
      - img "Mini Map"
      - link "React Flow attribution":
        - /url: https://reactflow.dev
        - text: React Flow
    - button "Smart align":
      - img
```

# Test source

```ts
  81  |         { 
  82  |             id: 'r1_router', type: 'router', ref_id: 'new', x: 450, y: 150, 
  83  |             config: { 
  84  |                 label: 'Priority Switch', 
  85  |                 rules: JSON.stringify([
  86  |                     { label: 'urgent', field: 'priority', operator: 'eq', value: 'high' }
  87  |                 ])
  88  |             } 
  89  |         },
  90  |         
  91  |         // Node 3: Filter (If)
  92  |         { 
  93  |             id: 't2_filter', type: 'transformation', ref_id: 'new', x: 650, y: 50, 
  94  |             config: { transType: 'filter_data', field: 'priority', operator: 'eq', value: 'high', label: 'If High Priority' } 
  95  |         },
  96  |         
  97  |         // Node 4: DB Lookup (Multiple fields)
  98  |         { 
  99  |             id: 'db1', type: 'transformation', ref_id: 'new', x: 850, y: 50, 
  100 |             config: { 
  101 |                 transType: 'db_lookup', 
  102 |                 sourceId: setupData.lookupSrcId, 
  103 |                 table: 'lookup_table', 
  104 |                 keyColumn: 'user_name', 
  105 |                 keyField: '$.name', 
  106 |                 valueColumn: 'city, country', 
  107 |                 targetField: 'location',
  108 |                 flattenInto: '.',
  109 |                 label: 'User Info Lookup'
  110 |             } 
  111 |         },
  112 |         
  113 |         // Node 5: Advanced (Fields projection)
  114 |         { 
  115 |             id: 't3_fields', type: 'transformation', ref_id: 'new', x: 1050, y: 150, 
  116 |             config: { 
  117 |                 transType: 'advanced', 
  118 |                 'column.id': 'source.id',
  119 |                 'column.name': 'source.name',
  120 |                 'column.city': 'source.city',
  121 |                 'column.country': 'source.country',
  122 |                 'column.priority': 'source.priority',
  123 |                 'column.status': 'source.priority_label',
  124 |                 label: 'Fields Projection'
  125 |             } 
  126 |         },
  127 |         
  128 |         // Sink
  129 |         { id: 'snk1', type: 'sink', ref_id: setupData.sinkId, x: 1250, y: 150 }
  130 |       ],
  131 |       edges: [
  132 |         { id: 'e1', source_id: 'src1', target_id: 't1_map' },
  133 |         { id: 'e2', source_id: 't1_map', target_id: 'r1_router' },
  134 |         { id: 'e3', source_id: 'r1_router', target_id: 't2_filter', source_handle: 'urgent' },
  135 |         { id: 'e4', source_id: 't2_filter', target_id: 'db1' },
  136 |         { id: 'e5', source_id: 'db1', target_id: 't3_fields' },
  137 |         { id: 'e6', source_id: 'r1_router', target_id: 't3_fields', source_handle: 'default' },
  138 |         { id: 'e7', source_id: 't3_fields', target_id: 'snk1' }
  139 |       ]
  140 |     };
  141 | 
  142 |     const workflowRes = await page.evaluate(async (wf) => {
  143 |       const token = localStorage.getItem('hermod_token');
  144 |       const res = await fetch('/api/workflows', { 
  145 |         method: 'POST', 
  146 |         headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` }, 
  147 |         body: JSON.stringify(wf) 
  148 |       });
  149 |       return res.json();
  150 |     }, workflow);
  151 |     const workflowID = workflowRes.id;
  152 | 
  153 |     // 3. Verify Field Propagation in UI
  154 |     await page.goto(`http://localhost:5173/workflows/${workflowID}`);
  155 |     await page.waitForSelector('.react-flow__node');
  156 |     await page.waitForTimeout(5000); // Wait for layout to settle
  157 | 
  158 |     // Log nodes for debugging
  159 |     await page.evaluate(() => {
  160 |         const nodes = document.querySelectorAll('.react-flow__node');
  161 |         console.log(`Found ${nodes.length} nodes on canvas`);
  162 |         nodes.forEach(n => console.log(`Node: ${n.textContent}, ID: ${n.getAttribute('data-id')}`));
  163 |     });
  164 | 
  165 |     // Open T3 (Fields Projection) and check inferred fields
  166 |     await page.getByText('Fields Projection').first().click({ force: true, clickCount: 2 });
  167 |     await page.waitForTimeout(2000);
  168 |     
  169 |     // Try coordinate click if not visible
  170 |     const isModalVisible = await page.locator('div[role="dialog"]').isVisible();
  171 |     if (!isModalVisible) {
  172 |         const t3Node = page.locator('.react-flow__node', { hasText: 'Fields Projection' }).first();
  173 |         const box = await t3Node.boundingBox();
  174 |         if (box) {
  175 |             await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  176 |             await page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  177 |         }
  178 |     }
  179 |     
  180 |     // Wait for modal by title prefix
> 181 |     await expect(page.locator('div[role="dialog"]')).toContainText('Configure Transformation', { timeout: 20000 });
      |                                                      ^ Error: expect(locator).toContainText(expected) failed
  182 | 
  183 |     // Check if fields from DB Lookup are inferred
  184 |     await page.click('text=Add Field');
  185 |     const fieldInput = page.locator('input[placeholder="Select or enter field..."]').last();
  186 |     await fieldInput.click();
  187 |     await expect(page.locator('text=city (inferred)')).toBeVisible({ timeout: 5000 });
  188 |     await expect(page.locator('text=country (inferred)')).toBeVisible({ timeout: 5000 });
  189 |     await expect(page.locator('text=priority_label (inferred)')).toBeVisible({ timeout: 5000 });
  190 |     console.log('UI Field Propagation verified');
  191 | 
  192 |     // 4. Test Workflow Simulation (Accuracy check)
  193 |     // We use the /test API to verify the engine logic for this complex flow
  194 |     const testResults = await page.evaluate(async ({ workflowID }) => {
  195 |         const token = localStorage.getItem('hermod_token');
  196 |         const headers = { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` };
  197 |         
  198 |         const res = await fetch(`/api/workflows/${workflowID}/test`, {
  199 |             method: 'POST',
  200 |             headers,
  201 |             body: JSON.stringify({
  202 |                 message: { id: 'msg-1', name: 'John Doe', priority: 'high', msg: 'urgent' }
  203 |             })
  204 |         });
  205 |         return res.json();
  206 |     }, { workflowID });
  207 | 
  208 |     // Verify last step output
  209 |     const lastStep = testResults.find((s: any) => s.node_id === 'snk1');
  210 |     expect(lastStep).toBeDefined();
  211 |     expect(lastStep.payload.name).toBe('John Doe');
  212 |     expect(lastStep.payload.city).toBe('New York');
  213 |     expect(lastStep.payload.country).toBe('USA');
  214 |     expect(lastStep.payload.status).toBe('URGENT');
  215 |     console.log('Workflow Accuracy verified via Test API');
  216 | 
  217 |     // 5. Check for after.id 'sample-' prefix (fixed in previous step)
  218 |     const hasSampleId = testResults.some((step: any) => 
  219 |         step.payload && step.payload.after && typeof step.payload.after.id === 'string' && step.payload.after.id.startsWith('sample-')
  220 |     );
  221 |     expect(hasSampleId).toBe(false);
  222 |     console.log('No "sample-" prefix detected in after.id');
  223 | 
  224 |     // 6. Test dynamic update: Change DB lookup and verify next node field list updates
  225 |     await page.reload(); // Close modal and refresh
  226 |     
  227 |     // Update DB Lookup to add another field 'age'
  228 |     await page.evaluate(async ({ workflowID }) => {
  229 |         const token = localStorage.getItem('hermod_token');
  230 |         const headers = { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` };
  231 |         const wf = await (await fetch(`/api/workflows/${workflowID}`)).json();
  232 |         const dbNode = wf.nodes.find((n: any) => n.id === 'db1');
  233 |         dbNode.data.config.valueColumn = 'city, country, age';
  234 |         await fetch(`/api/workflows/${workflowID}`, { method: 'PUT', headers, body: JSON.stringify(wf) });
  235 |     }, { workflowID });
  236 | 
  237 |     await page.reload();
  238 |     await page.waitForSelector('.react-flow__node', { timeout: 30000 });
  239 |     await page.waitForTimeout(5000);
  240 |     const t3NodeUpdated = page.locator('.react-flow__node', { hasText: 'Fields Projection' }).first();
  241 |     await t3NodeUpdated.click({ force: true });
  242 |     await page.click('text=Add Field');
  243 |     const fieldInputUpdated = page.locator('input[placeholder="Select or enter field..."]').last();
  244 |     await fieldInputUpdated.click();
  245 |     await expect(page.locator('text=age (inferred)')).toBeVisible({ timeout: 5000 });
  246 |     console.log('Dynamic field update verified');
  247 |   });
  248 | });
  249 | 
```