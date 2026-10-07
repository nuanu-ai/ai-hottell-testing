/**
 * Where to go after signing in: next when it is a path of this site, else / (the start page decides).
 * «//host» and «/\host» are read by browsers as another host, so they do not count.
 */
export function nextPath(next: string | undefined): string {
  if (next?.startsWith('/') && !next.startsWith('//') && !next.startsWith('/\\')) {
    return next;
  }
  return '/';
}
