const shellUnsafePattern = /[\s"'\\$`!#&*();<>?[\]^{|}~]/;

export function quoteShellArg(value: string) {
  const trimmedValue = value.trim();

  if (trimmedValue === "") {
    return "''";
  }

  if (!shellUnsafePattern.test(trimmedValue)) {
    return trimmedValue;
  }

  return `'${trimmedValue.replace(/'/g, `'\\''`)}'`;
}

export function quoteShellArgs(args: string[]) {
  return args.map(quoteShellArg).join(" ");
}

export function quotePowerShellArg(value: string) {
  return `'${value.trim().replace(/'/g, "''")}'`;
}