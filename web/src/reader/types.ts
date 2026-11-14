export type ReaderEntry = {
  id: number;
  title: string;
  japaneseTitle?: string;
  artists: string[];
  language: string;
  type: string;
  pages: number;
  tags: { name: string; namespace: string }[];
};
export type ReaderLibrary = {
  entries: ReaderEntry[];
  tags: { value: string; count: number }[];
  total: number;
  page: number;
  pageSize: number;
  scanning: boolean;
  found: number;
};
export type ReaderWork = ReaderEntry & { pageList: { w: number; h: number }[] };
