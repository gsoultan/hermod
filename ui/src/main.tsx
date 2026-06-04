import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { MantineProvider, createTheme } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import '@mantine/notifications/styles.css'
import './index.css'
import App from './App.tsx'

const theme = createTheme({
  primaryColor: 'indigo',
  defaultRadius: 'md',
  colors: {
    dark: [
      '#C1C2C5', // [0]
      '#A6A7AB', // [1]
      '#909296', // [2]
      '#5C5F66', // [3]
      '#373A40', // [4]
      '#2C2E33', // [5]
      '#25262B', // [6] Surface background
      '#1A1B1E', // [7] App background
      '#141517', // [8]
      '#101113', // [9]
    ],
  },
  fontFamily: 'Inter, system-ui, -apple-system, sans-serif',
  headings: {
    fontFamily: 'Inter, system-ui, -apple-system, sans-serif',
    fontWeight: '700',
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
    Paper: {
      defaultProps: {
        radius: 'md',
      },
    },
    Table: {
      styles: {
        thead: {
          backgroundColor: 'var(--mantine-color-gray-0)',
          '[data-mantine-color-scheme="dark"] &': {
            backgroundColor: 'var(--mantine-color-dark-7)',
          },
        },
        th: {
          borderBottom: '1px solid var(--mantine-color-gray-2)',
          '[data-mantine-color-scheme="dark"] &': {
            borderBottom: '1px solid var(--mantine-color-dark-4)',
          },
        },
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
