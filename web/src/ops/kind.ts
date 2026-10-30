/** The server may spell an operation kind "shared-up", "shared_up" or "shared.up". */
export const normKind = (kind: string): string => kind.replace(/[_.]/g, "-");
