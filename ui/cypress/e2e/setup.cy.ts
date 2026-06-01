describe('Setup Wizard', () => {
  beforeEach(() => {
    // Reset state if possible, or just visit setup
    cy.visit('/setup')
  })

  it('should complete the setup wizard', () => {
    // Check if we are on the setup page
    cy.contains('Initial System Setup').should('be.visible')
    
    // Step 1: Database configuration
    cy.contains('Database Configuration').should('be.visible')
    cy.get('select').select('SQLite')
    cy.get('input[placeholder="hermod_state.db"]').should('have.value', 'hermod_state.db')
    cy.get('button').contains('Next').click()

    // Step 2: System configuration
    cy.contains('System Configuration').should('be.visible')
    cy.get('input[label="Cluster Name"]').clear().type('Test Cluster')
    cy.get('button').contains('Next').click()

    // Step 3: Admin user setup
    cy.contains('Admin User Setup').should('be.visible')
    cy.get('input[placeholder="admin"]').clear().type('admin')
    cy.get('input[placeholder="Your password"]').type('password123')
    cy.get('button').contains('Complete Setup').click()

    // Should redirect to login or dashboard
    cy.url().should('include', '/login')
  })
})
