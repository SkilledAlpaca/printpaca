// Builds a geometry-only 3MF (a 10 mm cube, no slicer metadata) as an in-memory stored ZIP, so specs don't
// depend on a real file in the gitignored testdata/ folder. The reader doesn't validate the CRC, so it is 0.
const MODEL_XML = `<?xml version="1.0" encoding="UTF-8"?><model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources><object id="1" type="model"><mesh><vertices><vertex x="0" y="0" z="0"/><vertex x="10" y="0" z="0"/><vertex x="10" y="10" z="0"/><vertex x="0" y="10" z="0"/><vertex x="0" y="0" z="10"/><vertex x="10" y="0" z="10"/><vertex x="10" y="10" z="10"/><vertex x="0" y="10" z="10"/></vertices><triangles><triangle v1="0" v2="3" v3="2"/><triangle v1="0" v2="2" v3="1"/><triangle v1="4" v2="5" v3="6"/><triangle v1="4" v2="6" v3="7"/><triangle v1="0" v2="1" v3="5"/><triangle v1="0" v2="5" v3="4"/><triangle v1="3" v2="7" v3="6"/><triangle v1="3" v2="6" v3="2"/><triangle v1="0" v2="4" v3="7"/><triangle v1="0" v2="7" v3="3"/><triangle v1="1" v2="2" v3="6"/><triangle v1="1" v2="6" v3="5"/></triangles></mesh></object></resources><build><item objectid="1"/></build></model>`;

export function geometryOnly3mf(): Buffer {
  const name = Buffer.from("3D/3dmodel.model");
  const data = Buffer.from(MODEL_XML);
  const local = Buffer.alloc(30);
  local.writeUInt32LE(0x04034b50, 0);
  local.writeUInt16LE(20, 4);
  local.writeUInt32LE(data.length, 18);
  local.writeUInt32LE(data.length, 22);
  local.writeUInt16LE(name.length, 26);
  const central = Buffer.alloc(46);
  central.writeUInt32LE(0x02014b50, 0);
  central.writeUInt16LE(20, 4);
  central.writeUInt16LE(20, 6);
  central.writeUInt32LE(data.length, 20);
  central.writeUInt32LE(data.length, 24);
  central.writeUInt16LE(name.length, 28);
  const localPart = Buffer.concat([local, name, data]);
  const centralPart = Buffer.concat([central, name]);
  const eocd = Buffer.alloc(22);
  eocd.writeUInt32LE(0x06054b50, 0);
  eocd.writeUInt16LE(1, 8);
  eocd.writeUInt16LE(1, 10);
  eocd.writeUInt32LE(centralPart.length, 12);
  eocd.writeUInt32LE(localPart.length, 16);
  return Buffer.concat([localPart, centralPart, eocd]);
}
