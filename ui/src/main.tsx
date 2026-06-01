import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { MantineProvider, createTheme } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import '@mantine/notifications/styles.css'
import './index.css'
import App from './App.tsx'

const theme = createTheme({
  primaryColor: 'indigo',
  colors: {
    dark: [
      '#d5d7e0',
      '#acaebf',
      '#8c8fa3',
      '#666980',
      '#4d4f66',
      '#34354a',
      '#18191c', // [6] Surface background
      '#111111', // [7] App background
      '#0d0d0d', // [8]
      '#050505', // [9]
    ],
  },
  fontFamily: 'Inter, system-ui, -apple-system, sans-serif',
  headings: {
    fontFamily: 'Inter, system-ui, -apple-system, sans-serif',
  },
  components: {
    Button: {
      defaultProps: {
        radius: 'md',
      },
    },
    NavLink: {
      styles: {
        root: {
          borderRadius: 'var(--mantine-radius-md)',
          marginBottom: 'var(--mantine-spacing-xs)',
          '&[data-active]': {
            fontWeight: 600,
          },
        },
      },
    },
    Card: {
      defaultProps: {
        radius: 'md',
        withBorder: true,
      },
    },
  },
});

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <MantineProvider theme={theme} defaultColorScheme="dark">
      <Notifications position="top-right" zIndex={2000} />
      <App />
    </MantineProvider>
  </StrictMode>,
)
