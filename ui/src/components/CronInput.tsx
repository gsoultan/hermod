import { useState, useEffect } from 'react';import { IconCalendarStats, IconInfoCircle } from '@tabler/icons-react';
import { 
  TextInput, Button, Popover, Stack, Group, Select, Text, 
  SegmentedControl, 
  Divider, Box, ActionIcon, Tooltip
} from '@mantine/core';interface CronInputProps {
  label: string;
  value: string;
  onChange: (value: string) => void;
  description?: string;
  required?: boolean;
  placeholder?: string;
}

const PRESETS = [
  { label: 'Every Minute', value: '* * * * *' },
  { label: 'Every 5 Minutes', value: '*/5 * * * *' },
  { label: 'Every 15 Minutes', value: '*/15 * * * *' },
  { label: 'Every 30 Minutes', value: '*/30 * * * *' },
  { label: 'Every Hour', value: '0 * * * *' },
  { label: 'Every Day at Midnight', value: '0 0 * * *' },
  { label: 'Every Week (Sunday)', value: '0 0 * * 0' },
  { label: 'Every Month (1st)', value: '0 0 1 * *' },
];

export function CronInput({ label, value, onChange, description, required, placeholder }: CronInputProps) {
  const [opened, setOpened] = useState(false);
  const [mode, setMode] = useState('presets');
  
  // Internal state for builder
  const [minutes, setMinutes] = useState('0');
  const [hours, setHours] = useState('0');
  const [days, setDays] = useState('*');
  const [months, setMonths] = useState('*');
  const [weekdays, setWeekdays] = useState('*');

  useEffect(() => {
    if (value && value.split(' ').length === 5) {
      const parts = value.split(' ');
      setMinutes(parts[0]);
      setHours(parts[1]);
      setDays(parts[2]);
      setMonths(parts[3]);
      setWeekdays(parts[4]);
    }
  }, [value]);

  const applyPreset = (preset: string) => {
    onChange(preset);
    setOpened(false);
  };

  const handleBuild = () => {
    const expr = `${minutes} ${hours} ${days} ${months} ${weekdays}`;
    onChange(expr);
    setOpened(false);
  };

  return (
    <Box mb="md">
      <TextInput
        label={label}
        placeholder={placeholder || "*/5 * * * *"}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        required={required}
        description={description}
        rightSection={
          <Popover opened={opened} onChange={setOpened} position="bottom-end" withArrow shadow="md" width={320}>
            <Popover.Target>
              <Tooltip label="Schedule Builder">
                <ActionIcon variant="subtle" color="blue" onClick={() => setOpened((o) => !o)}>
                  <IconCalendarStats size="1.2rem" />
                </ActionIcon>
              </Tooltip>
            </Popover.Target>
            <Popover.Dropdown p="md">
              <Stack gap="sm">
                <Text fw={700} size="sm">Cron Schedule Builder</Text>
                
                <SegmentedControl
                  value={mode}
                  onChange={setMode}
                  data={[
                    { label: 'Presets', value: 'presets' },
                    { label: 'Build', value: 'build' },
                  ]}
                  fullWidth
                  size="xs"
                />

                {mode === 'presets' ? (
                  <Stack gap={4}>
                    {PRESETS.map((p) => (
                      <Button 
                        key={p.value} 
                        variant="ghost" 
                        fullWidth 
                        justify="space-between" 
                        onClick={() => applyPreset(p.value)}
                        size="xs"
                        styles={{ inner: { width: '100%' } }}
                        rightSection={<Text size="xs" c="dimmed" ff="monospace">{p.value}</Text>}
                      >
                        <Text size="xs">{p.label}</Text>
                      </Button>
                    ))}
                  </Stack>
                ) : (
                  <Stack gap="xs">
                    <Select
                      label="Minute"
                      size="xs"
                      data={[
                        { value: '*', label: 'Every minute (*)' },
                        { value: '0', label: 'At start of minute (0)' },
                        { value: '*/5', label: 'Every 5 minutes (*/5)' },
                        { value: '*/15', label: 'Every 15 minutes (*/15)' },
                        { value: '*/30', label: 'Every 30 minutes (*/30)' },
                      ]}
                      value={minutes}
                      onChange={(val) => setMinutes(val || '*')}
                    />
                    <Select
                      label="Hour"
                      size="xs"
                      data={[
                        { value: '*', label: 'Every hour (*)' },
                        { value: '0', label: 'Midnight (0)' },
                        { value: '12', label: 'Noon (12)' },
                        { value: '*/2', label: 'Every 2 hours (*/2)' },
                        { value: '*/4', label: 'Every 4 hours (*/4)' },
                        { value: '*/6', label: 'Every 6 hours (*/6)' },
                        { value: '*/12', label: 'Every 12 hours (*/12)' },
                      ]}
                      value={hours}
                      onChange={(val) => setHours(val || '*')}
                    />
                    <Select
                      label="Day of Month"
                      size="xs"
                      data={[
                        { value: '*', label: 'Every day (*)' },
                        { value: '1', label: '1st of month' },
                        { value: '15', label: '15th of month' },
                        { value: 'L', label: 'Last day of month' },
                      ]}
                      value={days}
                      onChange={(val) => setDays(val || '*')}
                    />
                    <Select
                      label="Month"
                      size="xs"
                      data={[
                        { value: '*', label: 'Every month (*)' },
                        { value: '*/2', label: 'Every 2 months (*/2)' },
                        { value: '*/3', label: 'Every quarter (*/3)' },
                        { value: '1', label: 'January' },
                        { value: '6', label: 'June' },
                        { value: '12', label: 'December' },
                      ]}
                      value={months}
                      onChange={(val) => setMonths(val || '*')}
                    />
                    <Select
                      label="Day of Week"
                      size="xs"
                      data={[
                        { value: '*', label: 'Every day (*)' },
                        { value: '1-5', label: 'Monday to Friday' },
                        { value: '0,6', label: 'Weekends' },
                        { value: '0', label: 'Sunday' },
                        { value: '1', label: 'Monday' },
                      ]}
                      value={weekdays}
                      onChange={(val) => setWeekdays(val || '*')}
                    />
                    <Button mt="xs" size="xs" onClick={handleBuild}>Apply Expression</Button>
                  </Stack>
                )}
                
                <Divider />
                <Group gap={4} wrap="nowrap">
                  <IconInfoCircle size="0.8rem" color="gray" />
                  <Text size="xs" c="dimmed">Expression: {minutes} {hours} {days} {months} {weekdays}</Text>
                </Group>
              </Stack>
            </Popover.Dropdown>
          </Popover>
        }
      />
    </Box>
  );
}


