describe('Login Page', () => {
  it('should load the login page', () => {
    cy.visit('/login')
    cy.contains('Welcome back').should('be.visible')
    cy.get('input[placeholder="Your username"]').should('be.visible')
    cy.get('input[placeholder="Your password"]').should('be.visible')
    cy.get('button[type="submit"]').should('be.visible').contains('Sign In')
  })
})
