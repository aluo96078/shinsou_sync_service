/** The package a work comes from, with the source id when the package has more than one name for it. */
export function packageLabel(work: { packageId: string; sourceId: string }): string {
  return work.sourceId && work.sourceId !== work.packageId ? `${work.packageId} / ${work.sourceId}` : work.packageId
}
