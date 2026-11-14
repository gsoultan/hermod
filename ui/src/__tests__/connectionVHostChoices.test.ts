import { connectionVHostChoices } from '@/lib/vhostChoices'

// On a fresh install no virtual host has been created, yet everything already
// lives in `default`: a new flow is saved there, and the Secrets and AI budget
// pages offer it. The Add source and Add sink forms listed only created vhosts,
// so their Virtual host picker was empty, Next Step stayed disabled, and no
// source or sink could be added through the UI until someone created a vhost.
describe('connectionVHostChoices', () => {
  it('offers default to an administrator before any vhost exists', () => {
    expect(connectionVHostChoices('Administrator', [], [])).toEqual(['default'])
  })

  it('offers default first, then the created vhosts, once each', () => {
    expect(
      connectionVHostChoices('Administrator', [{ name: 'sales' }, { name: 'default' }, { name: 'ops' }], []),
    ).toEqual(['default', 'sales', 'ops'])
  })

  it('offers anyone else exactly the vhosts they were given', () => {
    expect(connectionVHostChoices('Editor', [{ name: 'sales' }], ['ops'])).toEqual(['ops'])
    expect(connectionVHostChoices('Viewer', [], [])).toEqual([])
  })
})
