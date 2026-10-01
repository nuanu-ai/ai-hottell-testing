// Joins the class names that are set, in order, so the markup reads like deploy's templates.
export function classNames(...names: (string | false | undefined)[]): string {
  return names.filter(Boolean).join(' ');
}
