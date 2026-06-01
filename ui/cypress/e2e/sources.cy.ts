describe('Source Management', () => {
  beforeEach(() => {
    cy.login()
  })

  it('should add a new source', () => {
    cy.visit('/sources')
    cy.get('button').contains('Add Source').click()
    cy.get('input[placeholder="Source name"]').type('Cypress Source')
    cy.get('select').select('PostgreSQL')
    cy.get('input[placeholder="host=localhost user=..."]').type('host=localhost port=5432 user=postgres password=password dbname=test sslmode=disable')
    cy.get('button').contains('Save Source').click()
    
    cy.contains('Cypress Source').should('be.visible')
  })

  it('should update a source', () => {
    cy.visit('/sources')
    cy.contains('Cypress Source').parents('tr').find('button').first().click()
    cy.get('input[placeholder="Source name"]').clear().type('Cypress Source Updated')
    cy.get('button').contains('Save Source').click()
    
    cy.contains('Cypress Source Updated').should('be.visible')
  })
})
