import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export type DbType = 'sqlite' | 'postgres' | 'mysql' | 'mariadb' | 'mongodb' | null

interface SetupConfigState {
  dbType: DbType
  dbConn: string
  cryptoMasterKey: string

  setDbType: (t: DbType) => void
  setDbConn: (conn: string) => void
  setCryptoMasterKey: (key: string) => void
  reset: () => void
}

export const useSetupStore = create<SetupConfigState>()(
  persist(
    (set) => ({
      // Important: no default to sqlite on first load
      dbType: null,
      dbConn: '',
      cryptoMasterKey: '',

      setDbType: (t) => set({ dbType: t }),
      setDbConn: (dbConn) => set({ dbConn }),
      setCryptoMasterKey: (cryptoMasterKey) => set({ cryptoMasterKey }),
      reset: () => set({ dbType: null, dbConn: '', cryptoMasterKey: '' }),
    }),
    {
      name: 'hermod_setup_config_v1',
      version: 1,
      // Use localStorage by default; suitable for remembering initial setup choices
      // No migrations required for now
    }
  )
)
