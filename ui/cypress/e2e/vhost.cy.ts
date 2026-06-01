describe('Virtual Host Management', () => {
  beforeEach(() => {
    cy.login()
  })

  it('should create a new virtual host', () => {
    cy.visit('/vhosts')
    cy.get('button').contains('Add VHost').click()
    cy.get('input[placeholder="e.g. production"]').type('cypress-test-vhost')
    cy.get('textarea[placeholder="Optional description"]').type('Created by Cypress')
    cy.get('button[type="submit"]').click()
    
    cy.contains('cypress-test-vhost').should('be.visible')
  })

  it('should update an existing virtual host', () => {
    cy.visit('/vhosts')
    cy.contains('cypress-test-vhost').parents('tr').find('button').first().click() // Assuming first button is Edit
    cy.get('textarea[placeholder="Optional description"]').clear().type('Updated by Cypress')
    cy.get('button[type="submit"]').click()
    
    cy.contains('Updated by Cypress').should('be.visible')
  })
})
