// Joins the class names that are set, in order, so the markup reads like the v3 classes.
export function classNames(...names: (string | false | undefined)[]): string {
  return names.filter(Boolean).join(' ');
}
