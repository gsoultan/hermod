declare module 'qrcode' {
  export function toDataURL(
    text: string,
    opts?: {
      width?: number;
      margin?: number;
      errorCorrectionLevel?: 'L' | 'M' | 'Q' | 'H';
      color?: { dark?: string; light?: string };
    }
  ): Promise<string>;
}
