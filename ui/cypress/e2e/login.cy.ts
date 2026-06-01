describe('Login Flow', () => {
  it('should load the login page', () => {
    cy.visit('/login')
    cy.contains('Welcome back').should('be.visible')
  })

  it('should login successfully with admin credentials', () => {
    cy.visit('/login')
    cy.get('input[placeholder="Your username"]').type('admin')
    cy.get('input[placeholder="Your password"]').type('password123')
    cy.get('button[type="submit"]').click()
    
    // Should redirect to dashboard
    cy.url().should('eq', Cypress.config().baseUrl + '/')
    cy.contains('Dashboard').should('be.visible')
  })
})
