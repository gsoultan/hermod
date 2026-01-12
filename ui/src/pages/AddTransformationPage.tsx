import { Container, Title, Stack, Breadcrumbs, Anchor } from '@mantine/core';
import { Link } from '@tanstack/react-router';
import { TransformationForm } from '../components/TransformationForm';

export default function AddTransformationPage() {
  const items = [
    { title: 'Transformations', href: '/transformations' },
    { title: 'Add', href: '#' },
  ].map((item, index) => (
    <Anchor component={Link} to={item.href} key={index}>
      {item.title}
    </Anchor>
  ));

  return (
    <Container size="md">
      <Stack gap="lg">
        <Breadcrumbs>{items}</Breadcrumbs>
        <Title order={2}>Add Transformation</Title>
        <TransformationForm />
      </Stack>
    </Container>
  );
}
