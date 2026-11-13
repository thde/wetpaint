/** Inflates a zlib (RFC 1950) stream using the platform's decompressor. */
export async function inflate(zlib: Uint8Array<ArrayBuffer>): Promise<Uint8Array> {
  const stream = new Blob([zlib]).stream().pipeThrough(new DecompressionStream('deflate'));
  return new Uint8Array(await new Response(stream).arrayBuffer());
}
