/**
 * Replace characters that cannot be encoded as valid UTF-8 for protobuf string fields
 * with the Unicode replacement character (U+FFFD) so messages still pass through.
 *
 * JavaScript strings may contain unpaired UTF-16 surrogates (U+D800–U+DFFF); these
 * are not valid Unicode scalar values and cannot be encoded as UTF-8 — gRPC/protobuf
 * marshaling rejects them. Valid surrogate pairs (emoji etc.) are preserved.
 */
export function stripInvalidUtf8(s: string): string {
  if (!s) return s;
  let out = "";
  for (let i = 0; i < s.length; i++) {
    const code = s.charCodeAt(i);
    if (code >= 0xd800 && code <= 0xdbff) {
      if (i + 1 < s.length) {
        const next = s.charCodeAt(i + 1);
        if (next >= 0xdc00 && next <= 0xdfff) {
          // Valid surrogate pair — keep as-is (encodes to a 4-byte UTF-8 sequence).
          out += s[i]! + s[i + 1]!;
          i++;
          continue;
        }
      }
      // Lone high surrogate: replace so the message still goes through.
      out += "�";
      continue;
    }
    if (code >= 0xdc00 && code <= 0xdfff) {
      // Lone low surrogate: replace.
      out += "�";
      continue;
    }
    out += s[i]!;
  }
  return out;
}

/** Normalize optional strings before placing them on llmd gRPC/protobuf wire messages. */
export function grpcSafeString(s: string | undefined | null): string {
  if (s == null || s === "") return s ?? "";
  return stripInvalidUtf8(s);
}
