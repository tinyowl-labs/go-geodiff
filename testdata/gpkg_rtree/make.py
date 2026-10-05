"""Regenerate the indexed GeoPackage fixtures with GDAL's Python bindings.

    python3 make.py

base.gpkg and modified.gpkg each have two layers with GDAL's default spatial
index (rtree_* tables and triggers). The modified copy inserts, updates and
deletes geometries, including empty and NULL ones, so applying base → modified
runs every R-tree trigger.
"""
import os
import shutil

from osgeo import ogr, osr

ogr.UseExceptions()
osr.UseExceptions()
here = os.path.dirname(os.path.abspath(__file__))
base = os.path.join(here, "base.gpkg")
modified = os.path.join(here, "modified.gpkg")
for path in (base, modified):
    if os.path.exists(path):
        os.remove(path)


def put(layer, fid, wkt, name=None):
    f = ogr.Feature(layer.GetLayerDefn())
    f.SetFID(fid)
    if wkt is not None:
        f.SetGeometry(ogr.CreateGeometryFromWkt(wkt))
    f.SetField("name", name or f"f{fid}")
    return f


srs = osr.SpatialReference()
srs.ImportFromEPSG(3857)
ds = ogr.GetDriverByName("GPKG").CreateDataSource(base)
shapes = ds.CreateLayer("shapes", srs, ogr.wkbUnknown)
points = ds.CreateLayer("points", srs, ogr.wkbPoint)
for layer in (shapes, points):
    layer.CreateField(ogr.FieldDefn("name", ogr.OFTString))

for fid, wkt in [
    (1, "LINESTRING (0 0, 10 0)"),
    (2, "POLYGON ((0 0, 4 0, 4 4, 0 4, 0 0), (1 1, 2 1, 2 2, 1 1))"),
    (3, "MULTIPOINT ((5 5), (-3 7))"),
    (4, "LINESTRING EMPTY"),
    (5, None),
    (6, "CIRCULARSTRING (0 0, 1 1, 2 0)"),
    (7, "LINESTRING Z (1 2 3, 4 5 6)"),
]:
    shapes.CreateFeature(put(shapes, fid, wkt))
for fid, wkt in [(1, "POINT (1 1)"), (2, "POINT (2 2)"), (3, "POINT EMPTY"), (4, None)]:
    points.CreateFeature(put(points, fid, wkt))
ds = None

shutil.copy2(base, modified)
ds = ogr.Open(modified, 1)
shapes = ds.GetLayerByName("shapes")
points = ds.GetLayerByName("points")
for fid, wkt in [
    (1, "LINESTRING (0 0, 10 1)"),  # moved
    (2, "POLYGON EMPTY"),  # to empty
    (3, None),  # to NULL
    (4, "LINESTRING (-1 -1, 1 1)"),  # empty to real
    (5, "POINT (8 9)"),  # NULL to real
    (6, "CIRCULARSTRING (0 0, 1 -1, 2 0, 3 1, 4 0)"),
]:
    shapes.SetFeature(put(shapes, fid, wkt))
shapes.DeleteFeature(7)
shapes.CreateFeature(put(shapes, 8, "MULTIPOLYGON (((20 20, 21 20, 21 21, 20 20)))"))
shapes.CreateFeature(put(shapes, 9, "POINT EMPTY"))
shapes.CreateFeature(put(shapes, 10, "COMPOUNDCURVE (CIRCULARSTRING (0 0, 1 1, 2 0), (2 0, 3 0))"))
points.SetFeature(put(points, 1, "POINT (-1 -2)"))
points.SetFeature(put(points, 3, "POINT (3 3)"))
points.SetFeature(put(points, 4, "POINT (4 4)"))
points.DeleteFeature(2)
points.CreateFeature(put(points, 5, "POINT (5 5)"))
ds = None
