import { render, screen, waitFor } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { http, HttpResponse } from 'msw';
import { server, signInAs } from '../test/setupTests';
import GlobalHealthPage from '@/pages/monitoring/GlobalHealthPage';

/**
 * Mesh health is the third screen that describes a worker.
 *
 * It read a `memory` field that held a fraction in 0..1 and rendered it as
 * `{node.memory.toFixed(1)} MB`, so a worker using half its memory appeared as
 * "0.5 MB" — a fraction with a unit bolted on, on a page whose entire job is
 * to say whether the fleet is healthy. There was no disk reading and no core
 * count either.
 */
function draw() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <GlobalHealthPage />
      </QueryClientProvider>
    </MantineProvider>,
  );
}

function meshHealthReturns(nodes: unknown[]) {
  server.use(http.get('*/api/infra/mesh-health', () => HttpResponse.json(nodes)));
}

const worker = {
  id: 'w1',
  name: 'worker-1',
  status: 'online',
  type: 'worker',
  workflows: 3,
  last_seen: new Date().toISOString(),
  cpu_usage: 0.25,
  memory_usage: 0.5,
  cpu_cores: 8,
  memory_total_bytes: 32 * 1024 ** 3,
  memory_used_bytes: 16 * 1024 ** 3,
  storage_total_bytes: 500 * 1024 ** 3,
  storage_used_bytes: 125 * 1024 ** 3,
};

describe('GlobalHealthPage resources', () => {
  beforeEach(() => {
    signInAs();
  });

  it('reports memory as a size rather than a fraction labelled MB', async () => {
    meshHealthReturns([worker]);
    draw();

    expect(await screen.findByText('16 / 32 GB')).toBeInTheDocument();
    expect(screen.queryByText(/0\.5 MB/)).not.toBeInTheDocument();
  });

  it('reports cores and the data disk, which it had no reading for at all', async () => {
    meshHealthReturns([worker]);
    draw();

    expect(await screen.findByText('8 cores')).toBeInTheDocument();
    expect(screen.getByText('125 / 500 GB')).toBeInTheDocument();
  });

  /*
   * A mesh cluster is a remote endpoint, not a machine this platform measures,
   * so it reports no resources. Averaging its zeros in with the workers' made
   * the fleet look idler the more clusters were registered.
   */
  it('weights the fleet CPU figure by cores and ignores nodes that report none', async () => {
    meshHealthReturns([
      { ...worker, id: 'small', cpu_cores: 2, cpu_usage: 1 },
      { ...worker, id: 'big', cpu_cores: 30, cpu_usage: 0 },
      { id: 'cluster', name: 'eu-west', status: 'online', type: 'cluster', workflows: 0, last_seen: new Date().toISOString() },
    ]);
    draw();

    // 2 busy cores of 32. An unweighted mean over the three rows says 33%.
    expect(await screen.findByText('6.3%')).toBeInTheDocument();
  });

  /*
   * A node that stopped reporting is excluded from the fleet figure, the same
   * way the dashboard's cluster totals count only workers seen in the last two
   * minutes. Counting a machine that may already be gone describes capacity
   * nobody has.
   */
  it('leaves an offline node out of the fleet totals', async () => {
    // Two online nodes at different loads, so the fleet figure is a number no
    // individual ring also shows and the assertion cannot pass by accident.
    meshHealthReturns([
      { ...worker, id: 'up-busy', cpu_cores: 10, cpu_usage: 0.5, status: 'online' },
      { ...worker, id: 'up-idle', cpu_cores: 10, cpu_usage: 0.1, status: 'online' },
      { ...worker, id: 'down', cpu_cores: 90, cpu_usage: 0, status: 'offline' },
    ]);
    draw();

    expect(await screen.findByText('of 20 cores')).toBeInTheDocument();
    // 6 busy cores of 20. Counting the offline node's 90 idle ones says 5%.
    expect(screen.getByText('30%')).toBeInTheDocument();
  });

  /*
   * Its capacity is still true — the machine has the cores it had — but its
   * utilisation is a reading from whenever it last spoke. A filled ring beside
   * an OFFLINE badge reads as live.
   */
  it('keeps an offline node capacity but drops its stale utilisation', async () => {
    meshHealthReturns([{ ...worker, id: 'down', status: 'offline' }]);
    draw();

    expect(await screen.findByText('8 cores')).toBeInTheDocument();
    expect(screen.getByText('16 / 32 GB')).toBeInTheDocument();
    expect(screen.queryByText('25%')).not.toBeInTheDocument();
    expect(screen.queryByText('50%')).not.toBeInTheDocument();
  });

  it('says there is no reading rather than zero when nothing reported a size', async () => {
    meshHealthReturns([
      { id: 'cluster', name: 'eu-west', status: 'online', type: 'cluster', workflows: 0, last_seen: new Date().toISOString() },
    ]);
    draw();

    await waitFor(() => expect(screen.getByText('eu-west')).toBeInTheDocument());
    expect(screen.queryByText('0 cores')).not.toBeInTheDocument();
    expect(screen.queryByText(/0 \/ 0/)).not.toBeInTheDocument();
  });
});
