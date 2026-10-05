import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { OtherSourceConfig } from '@/components/workflow/Source/OtherSourceConfig'
import { SourceSetupInstructions } from '@/components/workflow/Source/SourceSetupInstructions'

/**
 * The gRPC source form told an operator one thing: "Logical gRPC path for the
 * source." It did not say that the path is a value the client sends rather than
 * a URL (its placeholder, /api/grpc/my-source, was copied from the webhook
 * form, where the path *is* the URL), that Hermod serves one fixed service so
 * there is no .proto to upload, where the endpoint is, how the API key travels,
 * or that the reply does not wait for the sinks. Every one of those was a
 * question a real user asked.
 *
 * The form now carries the answers next to the field, with the contract and a
 * command built from the path that was typed.
 */
function renderConfig(config: Record<string, any>) {
  render(
    <MantineProvider>
      <OtherSourceConfig config={config} updateConfig={() => {}} sourceType="grpc" />
    </MantineProvider>,
  )
}

describe('the gRPC source form', () => {
  it('shows a path example that is a label, not an HTTP route', () => {
    renderConfig({})
    const path = screen.getByLabelText(/gRPC Path/i)
    expect(path).toHaveAttribute('placeholder', '/grpc/my-source')
  })

  it('says the path is what the client sends', () => {
    renderConfig({})
    expect(screen.getAllByText(/PublishRequest\.path/).length).toBeGreaterThan(0)
  })

  it('says there is no .proto to upload', () => {
    renderConfig({})
    expect(screen.getByText(/no \.proto to upload/i)).toBeInTheDocument()
  })

  it('says the reply does not wait for the sinks', () => {
    renderConfig({})
    // In the guide itself, not only in the contract's comments.
    const points = screen.getAllByRole('listitem').map((item) => item.textContent ?? '')
    expect(points.some((text) => /does not wait for transformations or sinks/i.test(text))).toBe(true)
  })

  // One call per record is not the only way in: a producer with many records
  // needs to know the stream exists, and that answers are matched by id.
  it('says there is a stream for many records', () => {
    renderConfig({})
    const points = screen.getAllByRole('listitem').map((item) => item.textContent ?? '')
    expect(points.some((text) => /PublishStream/.test(text) && /\bid\b/.test(text))).toBe(true)
  })

  it('builds the sample command from the configured path', () => {
    renderConfig({ path: '/grpc/orders' })
    const command = screen.getByTestId('grpc-sample-command')
    expect(command).toHaveTextContent('"path":"/grpc/orders"')
    expect(command).toHaveTextContent('hermod.source.grpc.v1.SourceService/Publish')
  })

  it('adds the key to the command only when the source has one, and never the key itself', () => {
    renderConfig({ path: '/grpc/orders' })
    expect(screen.getByTestId('grpc-sample-command')).not.toHaveTextContent('x-api-key')
  })

  it('names the metadata key a keyed source expects', () => {
    renderConfig({ path: '/grpc/orders', api_key: 'sesame-secret' })
    const command = screen.getByTestId('grpc-sample-command')
    expect(command).toHaveTextContent('x-api-key')
    expect(command).not.toHaveTextContent('sesame-secret')
    expect(screen.getByText(/gRPC metadata/i)).toBeInTheDocument()
  })

  // The contract is shown so a user can generate a client from it. A copy that
  // drifts from the file the server is built from is worse than none.
  it('shows the contract the server is built from', () => {
    renderConfig({})
    const onDisk = readFileSync(
      resolve(__dirname, '../../../pkg/comm/source/grpc/proto/source.proto'),
      'utf8',
    )
    expect(screen.getByTestId('grpc-contract').textContent).toBe(onDisk)
  })
})

describe('the setup instructions for a gRPC source', () => {
  it('explain how to call it instead of asking for a source type', () => {
    render(
      <MantineProvider>
        <SourceSetupInstructions
          sourceType="grpc"
          useCDCChecked={false}
          config={{ path: '/grpc/orders' }}
          updateConfig={() => {}}
        />
      </MantineProvider>,
    )
    expect(screen.queryByText(/Select a source type/i)).not.toBeInTheDocument()
    expect(screen.getByTestId('grpc-sample-command')).toHaveTextContent('"path":"/grpc/orders"')
  })
})
