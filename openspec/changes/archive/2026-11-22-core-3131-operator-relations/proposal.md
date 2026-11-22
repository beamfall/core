# Operator Media relations

CORE-3131 implements ADR-0068 amendment H for Entity→Media relation writes. A manager
can choose an existing Entity or create one by name, and can remove an existing link.
The Media's active bundle supplies the allowed kinds, relations, attributes, and cardinality.
The console needs a visible-Entity name picker and a relation read that reports operator
locks even after a bundle upgrade removes a declaration.
