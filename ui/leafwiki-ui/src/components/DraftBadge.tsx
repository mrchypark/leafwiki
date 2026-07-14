export function DraftBadge({ inherited = false }: { inherited?: boolean }) {
  return (
    <span className="draft-badge">
      {inherited ? 'Inherited draft' : 'Draft'}
    </span>
  )
}
