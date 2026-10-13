/*
Everything the interface says, in English, which is also the shape every other
language has to fill: a phrase missing from a translation fails to compile.
*/
export const en = {
  brand: "Convia",
  failure: {
    message: "Something went wrong.",
    reload: "Reload",
  },
};

export type Words = typeof en;
