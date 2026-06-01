describe('Workflow Management', () => {
  beforeEach(() => {
    cy.login()
  })

  it('should create a new workflow', () => {
    cy.visit('/workflows')
    cy.get('button').contains('Create Workflow').click()
    cy.get('input[placeholder="Workflow Name"]').type('Cypress Workflow')
    cy.get('button').contains('Save').click()
    
    cy.url().should('include', '/edit')
    cy.contains('Cypress Workflow').should('be.visible')
  })

  it('should start and stop a workflow', () => {
    cy.visit('/workflows')
    // Find the one we just created
    cy.contains('Cypress Workflow').parents('tr').find('a').contains('View').click()
    
    // Toggle start
    cy.get('button').contains('Start').click()
    cy.contains('Stop').should('be.visible')
    
    // Toggle stop
    cy.get('button').contains('Stop').click()
    cy.contains('Start').should('be.visible')
  })

  it('should edit a workflow without crashing (React #185 check)', () => {
    cy.visit('/workflows')
    cy.contains('Cypress Workflow').parents('tr').find('a').contains('Edit').click()
    
    // If it crashes, this will fail
    cy.contains('Workflow Editor').should('be.visible')
    cy.get('.react-flow__renderer').should('be.visible')
    
    // Try to interact slightly
    cy.get('input[placeholder="Workflow Name"]').clear().type('Cypress Workflow Stable')
    cy.get('button').contains('Save').click()
    cy.contains('Configuration saved').should('be.visible')
  })
})
