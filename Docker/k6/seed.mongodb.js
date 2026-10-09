// Compose runs this database seed job before starting the k6 benchmark.
// Only documents carrying this fixture's marker can be updated on repeat runs.
const fs = require('fs');
const pool = JSON.parse(fs.readFileSync('/scripts/seed-pool.json', 'utf8'));
const marker = 'shorts-k6-fixture-v1';
const databaseName = process.env.DB_NAME || 'shorts';
const targetURL = process.env.ORIGINAL_URL || 'https://example.com';
const user = encodeURIComponent(process.env.MONGO_INITDB_ROOT_USERNAME || '');
const password = encodeURIComponent(process.env.MONGO_INITDB_ROOT_PASSWORD || '');
const credentials = user ? `${user}:${password}@` : '';
const uri = process.env.MONGODB_URI || `mongodb://${credentials}db:27017/${databaseName}?authSource=admin`;

if (pool.length !== 10000 || new Set(pool.map((link) => link.code)).size !== 10000) {
  throw new Error('Seed fixture must contain exactly 10,000 distinct codes');
}
if (pool.filter((link) => link.type === 'custom').length !== 5000 ||
    pool.filter((link) => link.type === 'auto' && /^[0-9A-Za-z]{7}$/.test(link.code)).length !== 5000) {
  throw new Error('Seed fixture must contain 5,000 custom and 5,000 seven-character Base62 codes');
}

const database = new Mongo(uri).getDB(databaseName);
database.links.createIndex({ code: 1 }, { name: 'uniq_code', unique: true });
const now = new Date();

for (let offset = 0; offset < pool.length; offset += 500) {
  database.links.bulkWrite(pool.slice(offset, offset + 500).map((link) => ({
    updateOne: {
      filter: { code: link.code, benchmark_seed: marker },
      update: {
        $set: { original_url: targetURL, type: link.type, owner_id: 'k6-benchmark', is_disabled: false },
        $setOnInsert: { code: link.code, benchmark_seed: marker, created_at: now },
        $unset: { expires_at: '' },
      },
      upsert: true,
    },
  })), { ordered: true });
}

if (database.links.countDocuments({ benchmark_seed: marker }) !== 10000) {
  throw new Error('Seed verification failed');
}
print('Seeded 10,000 benchmark links: 5,000 custom and 5,000 deterministic Base62 codes.');
