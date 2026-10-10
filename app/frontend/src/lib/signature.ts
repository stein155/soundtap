// SPDX-License-Identifier: GPL-3.0-only
// SPDX-FileCopyrightText: 2026 SoundTap contributors

// splitSignature takes a picked file and, when picked with it, its <name>.sig (ADR 0048).
export async function splitSignature(
  files: FileList | null
): Promise<{ file: File | null; signature?: string }> {
  const all = Array.from(files ?? []);
  const sig = all.find((f) => f.name.toLowerCase().endsWith('.sig'));
  return {
    file: all.find((f) => f !== sig) ?? null,
    signature: sig ? (await sig.text()).trim() : undefined,
  };
}
