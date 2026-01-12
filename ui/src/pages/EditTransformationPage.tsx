import { Container, Title, Stack, Breadcrumbs, Anchor, Loader, Center } from '@mantine/core';
import { Link, useParams } from '@tanstack/react-router';
import { useQuery } from '@tanstack/react-query';
import { TransformationForm } from '../components/TransformationForm';
import { apiFetch } from '../api';

export default function EditTransformationPage() {
  const { id } = useParams({ from: '/transformations/$id/edit' });
  
  const { data, isLoading } = useQuery({
    queryKey: ['transformation', id],
    queryFn: async () => {
      const res = await apiFetch(`/api/transformations/${id}`);
      return res.json();
    }
  });

  const items = [
    { title: 'Transformations', href: '/transformations' },
    { title: 'Edit', href: '#' },
  ].map((item, index) => (
    <Anchor component={Link} to={item.href} key={index}>
      {item.title}
    </Anchor>
  ));

  if (isLoading) {
    return (
      <Center style={{ height: '50vh' }}>
        <Loader size="xl" />
      </Center>
    );
  }

  return (
    <Container size="md">
      <Stack gap="lg">
        <Breadcrumbs>{items}</Breadcrumbs>
        <Title order={2}>Edit Transformation: {data?.name}</Title>
        <TransformationForm initialData={data} isEditing={true} />
      </Stack>
    </Container>
  );
}
