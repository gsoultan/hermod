import { Badge, Tooltip } from '@mantine/core'
import { Link } from '@tanstack/react-router'
import { getSessionRole } from '@/auth/session'
import { roleColor, roleLabel, useInstance } from '@/lib/environments'

/**
 * Which environment this is, always in view, so nobody edits production
 * thinking it is staging. Nothing shows until an administrator sets a role.
 */
export function EnvironmentBadge() {
  const { data } = useInstance()
  const role = data?.role
  if (!role) return null
  const badge = (
    <Badge color={roleColor[role]} variant={role === 'production' ? 'filled' : 'light'} size="lg" radius="sm">
      {data.name || roleLabel[role]}
    </Badge>
  )
  return (
    <Tooltip label={`This is the ${roleLabel[role].toLowerCase()} instance`}>
      {getSessionRole() === 'Administrator'
        ? <Link to="/settings" search={{ tab: 'environments' } as any} style={{ textDecoration: 'none' }}>{badge}</Link>
        : badge}
    </Tooltip>
  )
}
