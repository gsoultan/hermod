describe('Sink Management', () => {
  beforeEach(() => {
    cy.login()
  })

  it('should add a new sink', () => {
    cy.visit('/sinks')
    cy.get('button').contains('Add Sink').click()
    cy.get('input[placeholder="Sink name"]').type('Cypress Sink')
    cy.get('select').select('PostgreSQL')
    cy.get('input[placeholder="host=localhost user=..."]').type('host=localhost port=5432 user=postgres password=password dbname=test sslmode=disable')
    cy.get('button').contains('Save Sink').click()
    
    cy.contains('Cypress Sink').should('be.visible')
  })

  it('should update a sink', () => {
    cy.visit('/sinks')
    cy.contains('Cypress Sink').parents('tr').find('button').first().click()
    cy.get('input[placeholder="Sink name"]').clear().type('Cypress Sink Updated')
    cy.get('button').contains('Save Sink').click()
    
    cy.contains('Cypress Sink Updated').should('be.visible')
  })
})
