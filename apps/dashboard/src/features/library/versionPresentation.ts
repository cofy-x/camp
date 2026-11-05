export function versionChecksLabel(status: string) {
  switch (status) {
    case 'passed':
      return 'Checks passed';
    case 'not_run':
      return 'Checks not run';
    case 'not_configured':
      return 'Checks not configured';
    default:
      return status ? `Checks: ${status}` : 'Checks not reported';
  }
}

export function visibleVersionChecks(status: string) {
  return status === 'not_run' || status === 'not_configured'
    ? null
    : versionChecksLabel(status);
}
