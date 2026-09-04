import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { MantineProvider, createTheme } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import '@mantine/notifications/styles.css'
import './index.css'
import App from './App.tsx'
import { ConfirmProvider } from './components/common/ConfirmProvider'

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
  // The system UI stack, deliberately.
  //
  // This said "Inter, system-ui, …" but no Inter was ever loaded — no
  // @font-face, no <link>, no .woff2 in the repo — so every user has always
  // been reading the fallback. Naming the fallback is the honest version, and
  // it is also the fastest: zero bytes on the critical path and no swap-in
  // reflow. On macOS this resolves to SF Pro and on Windows to Segoe UI
  // Variable, both of which are good UI faces.
  //
  // To actually adopt Inter: self-host the variable .woff2 under public/fonts,
  // declare @font-face with `font-display: swap` and a `size-adjust` tuned
  // against this stack's metrics, preload it in index.html, and put it back at
  // the front of both stacks here. Do not reach for a CDN <link> — it costs a
  // third-party connection before first paint.
  fontFamily:
    'system-ui, -apple-system, "Segoe UI Variable Text", "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif',
  fontFamilyMonospace:
    'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, "Liberation Mono", monospace',
  headings: {
    fontFamily:
      'system-ui, -apple-system, "Segoe UI Variable Display", "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif',
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
        // Flat properties only. Mantine's `styles` become inline styles, so a
        // nested selector like '&[data-active]' is not a CSS rule — React
        // rejects it as an unknown style property and silently drops it, which
        // is why active nav items rendered at weight 400 instead of 600. The
        // active weight lives in index.css, where the selector works.
        root: {
          borderRadius: 'var(--mantine-radius-md)',
          marginBottom: 'var(--mantine-spacing-xs)',
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
    Badge: {
      styles: {
        // Mantine truncates a Badge's label to its container, so a badge in a
        // narrow table cell rendered as "ACT…" or "3 NO…" — the shortest,
        // most-scanned labels in the app, unreadable. A badge is a small fixed
        // token: size it to its text and let the column give way instead.
        root: { flexShrink: 0, maxWidth: 'none' },
        // Mantine's xs badge label lands at 9-10px, below a comfortable
        // reading floor. Nudge the label up without changing badge geometry.
        label: {
          fontSize: 'calc(var(--mantine-font-size-xs) * 0.95)',
          overflow: 'visible',
          textOverflow: 'clip',
        },
      },
    },
  },
});

// The pre-hydration shell in index.html lives inside #root. createRoot clears
// its container on first mount, but removing it explicitly does not depend on
// that staying true, and it keeps the spinner out of the tree if the app throws
// before rendering.
document.getElementById('app-shell-fallback')?.remove()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <MantineProvider theme={theme} defaultColorScheme="dark">
      <Notifications position="bottom-right" zIndex={2000} />
      <ConfirmProvider>
        <App />
      </ConfirmProvider>
    </MantineProvider>
  </StrictMode>,
)
