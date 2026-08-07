import { QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { router } from './router'
import { Suspense } from 'react'
import { VHostProvider } from './context/VHostContext'
import { queryClient } from './lib/queryClient'

function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <VHostProvider>
        <Suspense>
          <RouterProvider router={router} />
        </Suspense>
      </VHostProvider>
    </QueryClientProvider>
  )
}

export default App
