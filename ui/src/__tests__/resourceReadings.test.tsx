import { render, screen } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { ResourceGauge } from '@/components/common/ResourceGauge';
import { ClusterResourceCards } from '@/components/system/ClusterResourceCards';
import { EMPTY_STATS } from '@/hooks/useDashboardStream';

function draw(ui: React.ReactNode) {
  return render(<MantineProvider>{ui}</MantineProvider>);
}

/**
 * The workers table showed two rings of percentages and nothing else: no core
 * count, no memory size, no disk at all. "80% CPU" is the same reading on a
 * two-core box and a sixty-four-core one, and a worker about to fill its data
 * disk looked exactly like one with a terabyte spare.
 */
describe('ResourceGauge', () => {
  it('shows the usage percentage and the capacity it is a share of', () => {
    draw(<ResourceGauge label="Memory" fraction={0.5} caption="16 / 32 GB" tooltip="Memory" />);

    expect(screen.getByText('50%')).toBeInTheDocument();
    expect(screen.getByText('16 / 32 GB')).toBeInTheDocument();
    expect(screen.getByText('Memory')).toBeInTheDocument();
  });

  /*
   * An unreported reading has to look different from a measured zero. A ring
   * drawn at 0% says the worker answered and answered "nothing", which for a
   * worker on an older release is a lie about the machine.
   */
  it('renders no reading rather than an empty ring when the worker did not say', () => {
    draw(<ResourceGauge label="Storage" fraction={null} caption="—" tooltip="Storage" />);

    expect(screen.queryByText('0%')).not.toBeInTheDocument();
    expect(screen.getAllByText('—').length).toBeGreaterThan(0);
  });

  it('turns red as a resource runs out, so a full disk is visible without reading the number', () => {
    const { container: calm } = draw(
      <ResourceGauge label="Storage" fraction={0.2} caption="100 / 500 GB" tooltip="Storage" />,
    );
    const { container: full } = draw(
      <ResourceGauge label="Storage" fraction={0.95} caption="475 / 500 GB" tooltip="Storage" />,
    );

    expect(calm.innerHTML).not.toEqual(full.innerHTML);
  });
});

/**
 * The dashboard had no resource reading at all — it could say how many workers
 * were online and nothing about what those workers were.
 */
describe('ClusterResourceCards', () => {
  const stats = {
    ...EMPTY_STATS,
    active_workers: 2,
    cpu_cores: 40,
    cpu_usage: 0.25,
    memory_total_bytes: 96 * 1024 ** 3,
    memory_used_bytes: 48 * 1024 ** 3,
    storage_total_bytes: 2 * 1024 ** 4,
    storage_used_bytes: 512 * 1024 ** 3,
  };

  it('reports cores, memory and storage with the share of each in use', () => {
    draw(<ClusterResourceCards stats={stats} />);

    expect(screen.getByText('40')).toBeInTheDocument();
    expect(screen.getByText(/25% in use/)).toBeInTheDocument();

    expect(screen.getByText('96 GB')).toBeInTheDocument();
    expect(screen.getByText(/48 GB used/)).toBeInTheDocument();

    // The total reads in TB and the used figure in GB, each in the unit that
    // keeps it legible, rather than forcing both into one and printing
    // "0.5 TB used" — which is a number the reader has to convert back.
    expect(screen.getByText('2 TB')).toBeInTheDocument();
    expect(screen.getByText(/512 GB used/)).toBeInTheDocument();
  });

  /*
   * With no worker online there is nothing to report, and a row of zeros would
   * read as a cluster of machines with no CPU and no disk rather than as an
   * empty cluster.
   */
  it('says there is no reading when no online worker reported a size', () => {
    draw(<ClusterResourceCards stats={EMPTY_STATS} />);

    expect(screen.queryByText('0 GB')).not.toBeInTheDocument();
    expect(screen.getAllByText('—').length).toBe(3);
  });
});

/*
 * Inside a table the column header already names the resource, so the gauge
 * omits its own label there. Three repeated labels are about 200px of row
 * width, which is what pushed the workers table's Description column off a
 * 1440px screen.
 */
describe('ResourceGauge without a label', () => {
  it('shows only the reading', () => {
    draw(<ResourceGauge fraction={0.25} caption="15 cores" tooltip="CPU" />);

    expect(screen.getByText('15 cores')).toBeInTheDocument();
    expect(screen.queryByText('CPU')).not.toBeInTheDocument();
  });
});
