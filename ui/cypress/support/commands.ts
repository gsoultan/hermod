// ***********************************************
// This example commands.ts shows you how to
// create various custom commands and overwrite
// existing commands.
//
// For more comprehensive examples of custom
// commands please read more here:
// https://on.cypress.io/custom-commands
// ***********************************************

declare global {
  namespace Cypress {
    interface Chainable {
      login(): Chainable<void>
    }
  }
}

Cypress.Commands.add('login', () => {
  cy.visit('/login')
  cy.get('input[placeholder="Your username"]').type('admin')
  cy.get('input[placeholder="Your password"]').type('password123')
  cy.get('button[type="submit"]').click()
  cy.url().should('eq', Cypress.config().baseUrl + '/')
})
