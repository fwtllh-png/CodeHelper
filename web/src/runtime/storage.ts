export interface BrowserProjectionState {
  cursor: number;
  selectedSessionID: string;
  drafts: Record<string, string>;
}

export interface BrowserStorage {
  load(scope: string): Promise<BrowserProjectionState | undefined>;
  save(scope: string, state: BrowserProjectionState): Promise<void>;
}

const databaseName = "codehelper-web";
const storeName = "projection";

export class IndexedDBBrowserStorage implements BrowserStorage {
  async load(scope: string): Promise<BrowserProjectionState | undefined> {
    const database = await openDatabase();
    if (!database) return undefined;
    try {
      return await requestValue<BrowserProjectionState | undefined>(
        database.transaction(storeName, "readonly").objectStore(storeName).get(scope)
      );
    } finally {
      database.close();
    }
  }

  async save(scope: string, state: BrowserProjectionState): Promise<void> {
    const database = await openDatabase();
    if (!database) return;
    try {
      const transaction = database.transaction(storeName, "readwrite");
      transaction.objectStore(storeName).put(structuredClone(state), scope);
      await transactionDone(transaction);
    } finally {
      database.close();
    }
  }
}

function openDatabase(): Promise<IDBDatabase | undefined> {
  if (typeof indexedDB === "undefined") {
    return Promise.resolve(undefined);
  }
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(databaseName, 1);
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(storeName)) {
        request.result.createObjectStore(storeName);
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error ?? new Error("open IndexedDB"));
    request.onblocked = () => reject(new Error("IndexedDB upgrade is blocked"));
  });
}

function requestValue<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error ?? new Error("read IndexedDB"));
  });
}

function transactionDone(transaction: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    transaction.oncomplete = () => resolve();
    transaction.onerror = () => reject(
      transaction.error ?? new Error("write IndexedDB")
    );
    transaction.onabort = () => reject(
      transaction.error ?? new Error("IndexedDB transaction aborted")
    );
  });
}
