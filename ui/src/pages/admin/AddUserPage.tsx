import { Paper } from '@mantine/core';
import { UserForm } from '@/components/forms/UserForm';
import { PageHeader } from '@/components/rows';

export function AddUserPage() {
  return (
    <>
      <PageHeader title="Add person" lede="Their sign-in, role and the virtual hosts they can work in" back={{ to: '/users', label: 'Back to people' }} />
      <Paper p="xl" withBorder radius="md">
        <UserForm />
      </Paper>
    </>
  );
}
